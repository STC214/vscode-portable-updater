package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	appClassName = "AutoUpdateVSCodeMainWindow"

	buttonAddID      = 100
	buttonUpdateID   = 101
	logEditID        = 102
	progressLabelID  = 103
	progressBarID    = 104
	workerLabelID    = 105
	workerSelectID   = 106
	workerHintID     = 107
	rowEditIDBase    = 1000
	rowDeleteIDBase  = 2000
	uiInvokeMessage  = WM_APP + 1
	progressBarClass = "msctls_progress32"
)

type PathRow struct {
	edit   HWND
	remove HWND
}

type App struct {
	hInstance HMODULE
	hwnd      HWND
	font      HFONT

	addButton            HWND
	updateButton         HWND
	progressBar          HWND
	progressLabel        HWND
	logEdit              HWND
	workerLabel          HWND
	workerHint           HWND
	workerSelect         HWND
	workerDropdownActive bool
	workerRestorePending bool

	rows   []PathRow
	config Config

	storageDir string
	cacheDir   string

	scrollPos int32
	busy      bool

	uiMu    sync.Mutex
	uiTasks []func()

	saveMu    sync.Mutex
	saveTimer *time.Timer
	saveSeq   uint64
}

var mainWndProc = syscall.NewCallback(windowProc)

var (
	pendingAppMu sync.Mutex
	pendingApp   *App
	windowApps   sync.Map
)

func NewApp() (*App, error) {
	if err := initCommonControls(); err != nil {
		return nil, err
	}

	hInstance, err := getModuleHandle()
	if err != nil {
		return nil, err
	}

	storageDir, cacheDir, err := ensureStorageDirs()
	if err != nil {
		return nil, err
	}

	_ = os.Setenv("TMP", cacheDir)
	_ = os.Setenv("TEMP", cacheDir)
	_ = os.Setenv("TMPDIR", cacheDir)
	_ = os.Setenv("GOTMPDIR", cacheDir)

	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}

	return &App{
		hInstance:  hInstance,
		font:       getDefaultGUIFont(),
		config:     cfg,
		storageDir: storageDir,
		cacheDir:   cacheDir,
	}, nil
}

func (a *App) Run() error {
	if err := registerClass(appClassName, mainWndProc, a.hInstance); err != nil {
		return err
	}

	pendingAppMu.Lock()
	pendingApp = a
	pendingAppMu.Unlock()

	hwnd, err := createWindowEx(
		0,
		appClassName,
		"VSCode 便携版自动升级",
		WS_OVERLAPPEDWINDOW|WS_VISIBLE|WS_VSCROLL,
		CW_USEDEFAULT,
		CW_USEDEFAULT,
		980,
		760,
		0,
		0,
		a.hInstance,
		0,
	)
	if err != nil {
		return err
	}
	a.hwnd = hwnd

	showWindow(hwnd, SW_SHOW)
	updateWindow(hwnd)

	var msg MSG
	for {
		ret, err := getMessage(&msg)
		if err != nil {
			return err
		}
		if ret == 0 {
			return nil
		}
		translateMessage(&msg)
		dispatchMessage(&msg)
	}
}

func windowProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	window := HWND(hwnd)
	if msg == WM_NCCREATE {
		pendingAppMu.Lock()
		app := pendingApp
		pendingApp = nil
		pendingAppMu.Unlock()
		if app != nil {
			app.hwnd = window
			windowApps.Store(window, app)
		}
	}

	value, ok := windowApps.Load(window)
	if !ok {
		return defWindowProc(window, msg, wParam, lParam)
	}
	app := value.(*App)
	return app.WndProc(window, msg, wParam, lParam)
}

func (a *App) WndProc(hwnd HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_GETMINMAXINFO:
		if lParam != 0 {
			var info *MINMAXINFO
			*(*uintptr)(unsafe.Pointer(&info)) = lParam
			info.MinTrackSize.X = max(info.MinTrackSize.X, 600)
			info.MinTrackSize.Y = max(info.MinTrackSize.Y, 320)
		}
		return 0
	case WM_CREATE:
		if err := a.initUI(); err != nil {
			showFatalError(fmt.Sprintf("创建界面失败: %v", err))
			return ^uintptr(0)
		}
		return 0
	case WM_SIZE:
		a.layout()
		return 0
	case WM_COMMAND:
		a.onCommand(wParam)
		return 0
	case WM_VSCROLL:
		a.onVScroll(lowWord(wParam))
		return 0
	case WM_MOUSEWHEEL:
		delta := int16(highWord(wParam))
		if delta > 0 {
			a.scrollBy(-48)
		} else if delta < 0 {
			a.scrollBy(48)
		}
		return 0
	case WM_CTLCOLOREDIT, WM_CTLCOLORSTATIC, WM_CTLCOLORLISTBOX:
		return a.handleCtlColor(msg, HDC(wParam), HWND(lParam))
	case WM_DRAWITEM:
		if a.handleDrawItem(lParam) {
			return 1
		}
		return 0
	case uiInvokeMessage:
		a.runUITasks()
		return 0
	case WM_CLOSE:
		if a.busy {
			a.appendLog("更新正在进行，请等待完成后再关闭窗口。")
			return 0
		}
		a.syncPathsFromUI()
		if err := a.saveConfigNow(); err != nil {
			a.appendLog(fmt.Sprintf("保存配置失败: %v", err))
		}
		destroyWindow(hwnd)
		return 0
	case WM_DESTROY:
		windowApps.Delete(hwnd)
		postQuitMessage(0)
		return 0
	default:
		return defWindowProc(hwnd, msg, wParam, lParam)
	}
}

func (a *App) initUI() error {
	var err error

	a.addButton, err = a.newButton("新增路径", buttonAddID)
	if err != nil {
		return err
	}
	a.updateButton, err = a.newButton("检查并自动更新", buttonUpdateID)
	if err != nil {
		return err
	}
	if err := a.initDownloadWorkerControls(); err != nil {
		return err
	}
	a.progressLabel, err = a.newControl("STATIC", "空闲 0%", WS_CHILD|WS_VISIBLE|SS_RIGHT, progressLabelID)
	if err != nil {
		return err
	}
	a.progressBar, err = a.newProgressBar(progressBarID)
	if err != nil {
		return err
	}
	a.logEdit, err = a.newEdit("", logEditID, ES_MULTILINE|ES_AUTOVSCROLL|ES_READONLY|WS_VSCROLL)
	if err != nil {
		return err
	}

	a.applyTheme()
	a.rebuildPathRows()
	a.layout()
	a.appendLog("程序已启动。")
	a.appendLog(fmt.Sprintf("配置目录: %s", a.storageDir))
	a.appendLog("所有缓存、下载和配置都只会写入程序所在盘符，不会写入 C 盘。")
	return nil
}

func (a *App) newButton(text string, id int) (HWND, error) {
	return a.newControl("BUTTON", text, WS_CHILD|WS_VISIBLE|WS_TABSTOP|BS_OWNERDRAW, id)
}

func (a *App) newLabel(text string, id int) (HWND, error) {
	return a.newControl("STATIC", text, WS_CHILD|WS_VISIBLE, id)
}

func (a *App) newProgressBar(id int) (HWND, error) {
	return a.newControl(progressBarClass, "", WS_CHILD|WS_VISIBLE|PBS_SMOOTH, id)
}

func (a *App) newEdit(text string, id int, extraStyle uint32) (HWND, error) {
	return a.newControl("EDIT", text, WS_CHILD|WS_VISIBLE|WS_TABSTOP|WS_BORDER|ES_LEFT|extraStyle, id)
}

func (a *App) newControl(className, text string, style uint32, id int) (HWND, error) {
	hwnd, err := createWindowEx(0, className, text, style, 0, 0, 0, 0, a.hwnd, HMENU(id), a.hInstance, 0)
	if err != nil {
		return 0, err
	}
	sendMessage(hwnd, WM_SETFONT, uintptr(a.font), 1)
	return hwnd, nil
}

func (a *App) rebuildPathRows() {
	for _, row := range a.rows {
		destroyWindow(row.edit)
		destroyWindow(row.remove)
	}
	a.rows = nil

	if len(a.config.Paths) == 0 {
		a.config.Paths = []string{""}
	}

	for i, path := range a.config.Paths {
		edit, err := a.newEdit(path, rowEditIDBase+i, 0)
		if err != nil {
			a.appendLog(fmt.Sprintf("创建路径输入框失败: %v", err))
			continue
		}
		remove, err := a.newButton("删除本行", rowDeleteIDBase+i)
		if err != nil {
			a.appendLog(fmt.Sprintf("创建删除按钮失败: %v", err))
			destroyWindow(edit)
			continue
		}
		a.rows = append(a.rows, PathRow{edit: edit, remove: remove})
	}
}

func (a *App) onCommand(wParam uintptr) {
	id := int(lowWord(wParam))
	code := highWord(wParam)

	switch {
	case id == workerSelectID:
		a.onDownloadWorkerSelection(code)
	case id == buttonAddID && code == BN_CLICKED:
		a.syncPathsFromUI()
		a.config.Paths = append(a.config.Paths, "")
		if err := a.saveConfigNow(); err != nil {
			a.appendLog(fmt.Sprintf("保存配置失败: %v", err))
		}
		a.rebuildPathRows()
		a.layout()
	case id == buttonUpdateID && code == BN_CLICKED:
		a.startUpdate()
	case id >= rowDeleteIDBase && id < rowDeleteIDBase+10000 && code == BN_CLICKED:
		a.deleteRow(id - rowDeleteIDBase)
	case id >= rowEditIDBase && id < rowEditIDBase+10000 && code == EN_CHANGE:
		index := id - rowEditIDBase
		if index >= 0 && index < len(a.rows) {
			a.config.Paths[index] = strings.TrimSpace(getWindowText(a.rows[index].edit))
			a.scheduleConfigSave()
		}
	}
}

func (a *App) deleteRow(index int) {
	a.syncPathsFromUI()
	if index < 0 || index >= len(a.config.Paths) {
		return
	}
	a.config.Paths = append(a.config.Paths[:index], a.config.Paths[index+1:]...)
	if len(a.config.Paths) == 0 {
		a.config.Paths = []string{""}
	}
	if err := a.saveConfigNow(); err != nil {
		a.appendLog(fmt.Sprintf("保存配置失败: %v", err))
	}
	a.rebuildPathRows()
	a.layout()
}

func (a *App) syncPathsFromUI() {
	if len(a.rows) == 0 {
		return
	}
	if len(a.config.Paths) != len(a.rows) {
		a.config.Paths = make([]string, len(a.rows))
	}
	for i, row := range a.rows {
		a.config.Paths[i] = strings.TrimSpace(getWindowText(row.edit))
	}
}

func (a *App) layout() {
	if a.hwnd == 0 {
		return
	}

	rect := getClientRect(a.hwnd)
	width := rect.Right - rect.Left
	height := rect.Bottom - rect.Top
	if width <= 0 || height <= 0 {
		return
	}

	margin := int32(12)
	buttonHeight := int32(34)
	rowHeight := int32(28)
	rowGap := int32(8)
	deleteWidth := int32(96)
	buttonGap := int32(10)
	geometry := calculateVerticalLayout(height)

	setWindowPos(a.addButton, margin, margin, 110, buttonHeight, SWP_NOZORDER|SWP_NOACTIVATE)
	setWindowPos(a.updateButton, margin+120, margin, 180, buttonHeight, SWP_NOZORDER|SWP_NOACTIVATE)
	setWindowPos(a.workerLabel, 336, margin+8, 64, 22, SWP_NOZORDER|SWP_NOACTIVATE)
	setWindowPos(a.workerSelect, 410, margin+5, 76, 240, SWP_NOZORDER|SWP_NOACTIVATE)
	setWindowPos(a.workerHint, 498, margin+8, 54, 22, SWP_NOZORDER|SWP_NOACTIVATE)
	statusWidth, statusY := int32(220), margin+8
	progressWidth := width - margin*2
	if width < 820 {
		statusWidth, statusY = 180, geometry.progressTop-5
		progressWidth = max(int32(0), progressWidth-statusWidth-12)
	}
	setWindowPos(a.progressLabel, width-margin-statusWidth, statusY, statusWidth, 22, SWP_NOZORDER|SWP_NOACTIVATE)
	setWindowPos(a.progressBar, margin, geometry.progressTop, progressWidth, 12, SWP_NOZORDER|SWP_NOACTIVATE)
	pathTop, logTop, pathHeight := geometry.pathTop, geometry.logTop, geometry.pathHeight

	setWindowPos(a.logEdit, margin, logTop, width-margin*2, geometry.logHeight, SWP_NOZORDER|SWP_NOACTIVATE)

	contentHeight := int32(len(a.rows)) * (rowHeight + rowGap)
	if contentHeight > 0 {
		contentHeight -= rowGap
	}
	maxScroll := contentHeight - pathHeight
	if maxScroll < 0 || pathHeight == 0 {
		maxScroll = 0
	}
	if a.scrollPos > maxScroll {
		a.scrollPos = maxScroll
	}

	scrollMaximum := max(int32(0), contentHeight-1) // Win32 maximum is inclusive.
	if pathHeight == 0 {
		scrollMaximum = 0
	}
	scrollInfo := SCROLLINFO{
		CbSize: uint32(unsafe.Sizeof(SCROLLINFO{})),
		FMask:  SIF_ALL,
		NMin:   0,
		NMax:   scrollMaximum,
		NPage:  uint32(pathHeight),
		NPos:   a.scrollPos,
	}
	setScrollInfo(a.hwnd, SB_VERT, &scrollInfo, true)

	editWidth := width - margin*2 - deleteWidth - buttonGap
	if editWidth < 200 {
		editWidth = 200
	}

	for i, row := range a.rows {
		y := pathTop + int32(i)*(rowHeight+rowGap) - a.scrollPos
		if y < pathTop || y+rowHeight > logTop-margin {
			showWindow(row.edit, SW_HIDE)
			showWindow(row.remove, SW_HIDE)
			continue
		}
		showWindow(row.edit, SW_SHOW)
		showWindow(row.remove, SW_SHOW)
		setWindowPos(row.edit, margin, y, editWidth, rowHeight, SWP_NOZORDER|SWP_NOACTIVATE)
		setWindowPos(row.remove, margin+editWidth+buttonGap, y, deleteWidth, rowHeight, SWP_NOZORDER|SWP_NOACTIVATE)
	}
}

func (a *App) onVScroll(code uint16) {
	switch code {
	case SB_LINEUP:
		a.scrollBy(-32)
	case SB_LINEDOWN:
		a.scrollBy(32)
	case SB_PAGEUP:
		a.scrollBy(-120)
	case SB_PAGEDOWN:
		a.scrollBy(120)
	case SB_TOP:
		a.setScrollPos(0)
	case SB_BOTTOM:
		a.setScrollPos(a.maxScroll())
	case SB_THUMBPOSITION, SB_THUMBTRACK:
		info := SCROLLINFO{
			CbSize: uint32(unsafe.Sizeof(SCROLLINFO{})),
			FMask:  SIF_TRACKPOS,
		}
		getScrollInfo(a.hwnd, SB_VERT, &info)
		a.setScrollPos(info.NTrackPos)
	}
}

func (a *App) scrollBy(delta int32) {
	a.setScrollPos(a.scrollPos + delta)
}

func (a *App) setScrollPos(pos int32) {
	if pos < 0 {
		pos = 0
	}
	maxScroll := a.maxScroll()
	if pos > maxScroll {
		pos = maxScroll
	}
	if pos == a.scrollPos {
		return
	}
	a.scrollPos = pos
	a.layout()
}

func (a *App) maxScroll() int32 {
	rect := getClientRect(a.hwnd)
	height := rect.Bottom - rect.Top
	rowHeight := int32(28)
	rowGap := int32(8)

	pathHeight := calculateVerticalLayout(height).pathHeight

	contentHeight := int32(len(a.rows)) * (rowHeight + rowGap)
	if contentHeight > 0 {
		contentHeight -= rowGap
	}
	maxScroll := contentHeight - pathHeight
	if maxScroll < 0 || pathHeight == 0 {
		maxScroll = 0
	}
	return maxScroll
}

func (a *App) appendLog(line string) {
	text := time.Now().Format("2006-01-02 15:04:05.000") + " " + strings.TrimRight(line, "\r\n") + "\r\n"
	if a.storageDir != "" {
		if err := appendLogFile(a.storageDir, text); err != nil {
			text += fmt.Sprintf("日志落盘失败: %v\r\n", err)
		}
	}
	if a.logEdit == 0 {
		return
	}
	sendMessage(a.logEdit, EM_SETSEL, ^uintptr(0), ^uintptr(0))
	sendMessage(a.logEdit, EM_REPLACESEL, 0, uintptr(unsafe.Pointer(mustUTF16Ptr(text))))
}

// Daily files keep historical runs inspectable without mixing GUI state.
func appendLogFile(storageDir, text string) error {
	logDir := filepath.Join(storageDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(logDir, time.Now().Format("2006-01-02")+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(text)
	return errors.Join(writeErr, f.Close())
}

func (a *App) invokeUI(fn func()) {
	a.uiMu.Lock()
	a.uiTasks = append(a.uiTasks, fn)
	a.uiMu.Unlock()
	postMessage(a.hwnd, uiInvokeMessage, 0, 0)
}

func (a *App) runUITasks() {
	a.uiMu.Lock()
	tasks := a.uiTasks
	a.uiTasks = nil
	a.uiMu.Unlock()

	for _, task := range tasks {
		task()
	}
}

func (a *App) scheduleConfigSave() {
	cfg := cloneConfig(a.config)

	a.saveMu.Lock()
	a.saveSeq++
	seq := a.saveSeq
	if a.saveTimer != nil {
		a.saveTimer.Stop()
	}
	a.saveTimer = time.AfterFunc(400*time.Millisecond, func() {
		a.saveMu.Lock()
		defer a.saveMu.Unlock()
		isLatest := seq == a.saveSeq
		if isLatest {
			a.saveTimer = nil
		}

		if !isLatest {
			return
		}

		if err := saveConfig(cfg); err != nil {
			a.invokeUI(func() {
				a.appendLog(fmt.Sprintf("保存配置失败: %v", err))
			})
		}
	})
	a.saveMu.Unlock()
}

func (a *App) saveConfigNow() error {
	cfg := cloneConfig(a.config)

	a.saveMu.Lock()
	defer a.saveMu.Unlock()
	a.saveSeq++
	if a.saveTimer != nil {
		a.saveTimer.Stop()
		a.saveTimer = nil
	}
	return saveConfig(cfg)
}

func (a *App) startUpdate() {
	if a.busy {
		return
	}

	a.syncPathsFromUI()
	var paths []string
	for _, path := range a.config.Paths {
		path = strings.TrimSpace(path)
		if path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		a.appendLog("没有可更新的路径，请先输入 VSCode 目录。")
		return
	}

	workers := a.config.DownloadWorkers
	a.setUpdateBusy(true)
	a.setProgress("准备更新", 0)
	if err := a.saveConfigNow(); err != nil {
		a.appendLog(fmt.Sprintf("保存配置失败: %v", err))
	}

	go func(paths []string, workers int) {
		updater := NewUpdaterWithWorkers(
			a.cacheDir,
			workers,
			func(message string) {
				a.invokeUI(func() {
					a.appendLog(message)
				})
			},
			func(progress ProgressUpdate) {
				a.invokeUI(func() {
					a.setProgress(progress.Label, progress.Percent)
				})
			},
		)

		start := time.Now()
		err := updater.UpdateAll(paths)
		a.invokeUI(func() {
			a.setUpdateBusy(false)
			if err != nil {
				a.setProgress("更新失败", 0)
				a.appendLog(fmt.Sprintf("更新结束，存在错误: %v", err))
			} else {
				a.setProgress("更新完成", 100)
				a.appendLog(fmt.Sprintf("更新完成，总耗时 %s", time.Since(start).Round(time.Second)))
			}
		})
	}(paths, workers)
}

func normalizeInputPath(path string) string {
	clean := filepath.Clean(strings.TrimSpace(path))
	if strings.EqualFold(filepath.Base(clean), "Code.exe") {
		return filepath.Dir(clean)
	}
	return clean
}
