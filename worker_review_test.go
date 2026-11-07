package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"unsafe"
)

const testCBShowDropdown = 0x014F
const testCBGetDroppedState = 0x0157
const testCBNSelEndOK = 9
const testCBNSelEndCancel = 10

var reviewWorkerHosts sync.Map
var reviewWorkerResizeHosts sync.Map
var reviewWorkerEvents sync.Map
var reviewWorkerClassOnce sync.Once
var reviewWorkerClassError error
var reviewWorkerWndProc = syscall.NewCallback(func(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	if msg == WM_SIZE {
		if _, enabled := reviewWorkerResizeHosts.Load(HWND(hwnd)); enabled {
			if value, ok := reviewWorkerHosts.Load(HWND(hwnd)); ok {
				value.(*App).layout()
				return 0
			}
		}
	}
	if msg == uiInvokeMessage {
		if value, ok := reviewWorkerHosts.Load(HWND(hwnd)); ok {
			value.(*App).runUITasks()
			return 0
		}
	}
	if msg == WM_COMMAND {
		if value, ok := reviewWorkerHosts.Load(HWND(hwnd)); ok {
			a := value.(*App)
			if int(lowWord(wParam)) == workerSelectID {
				if trace, ok := reviewWorkerEvents.Load(HWND(hwnd)); ok {
					events := trace.(*[]string)
					*events = append(*events, fmt.Sprintf("before code=%d selected=%d dropped=%d", highWord(wParam), a.selectedDownloadWorkers(), sendMessage(a.workerSelect, testCBGetDroppedState, 0, 0)))
				}
			}
			a.onCommand(wParam)
			return 0
		}
	}
	return defWindowProc(HWND(hwnd), msg, wParam, lParam)
})

func reviewWorkerApp(t *testing.T, rows int) *App {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	instance, err := getModuleHandle()
	if err != nil {
		t.Fatal(err)
	}
	if err := initCommonControls(); err != nil {
		t.Fatal(err)
	}
	reviewWorkerClassOnce.Do(func() {
		reviewWorkerClassError = registerClass("AutoUpdateWorkerReviewHost", reviewWorkerWndProc, instance)
	})
	if reviewWorkerClassError != nil {
		t.Fatal(reviewWorkerClassError)
	}
	parent, err := createWindowEx(0, "AutoUpdateWorkerReviewHost", "worker review", WS_OVERLAPPEDWINDOW, 0, 0, 980, 760, 0, 0, instance, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		reviewWorkerResizeHosts.Delete(parent)
		reviewWorkerHosts.Delete(parent)
		reviewWorkerEvents.Delete(parent)
		destroyWindow(parent)
	})
	paths := make([]string, rows)
	for i := range paths {
		paths[i] = fmt.Sprintf("path%d", i)
	}
	a := &App{hwnd: parent, hInstance: instance, font: getDefaultGUIFont(), config: cloneConfig(Config{Paths: paths})}
	reviewWorkerHosts.Store(parent, a)
	trace := []string{}
	reviewWorkerEvents.Store(parent, &trace)
	if err := a.initUI(); err != nil {
		t.Fatal(err)
	}
	return a
}

func preserveReviewConfig(t *testing.T) {
	t.Helper()
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := os.ReadFile(path)
	t.Cleanup(func() {
		if readErr == nil {
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Error(err)
			}
		} else if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Error(err)
		}
	})
}

func drainReviewWorkerMessages(a *App) {
	for {
		var msg MSG
		ret, _, _ := user32.NewProc("PeekMessageW").Call(uintptr(unsafe.Pointer(&msg)), uintptr(a.hwnd), uiInvokeMessage, uiInvokeMessage, 1)
		if ret == 0 {
			return
		}
		dispatchMessage(&msg)
	}
}

func TestReviewWorkerDropdownTransactions(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			preserveReviewConfig(t)
			a := reviewWorkerApp(t, 1)
			if err := saveConfig(a.config); err != nil {
				t.Fatal(err)
			}
			sendMessage(a.workerSelect, testCBShowDropdown, 1, 0)
			defer sendMessage(a.workerSelect, testCBShowDropdown, 0, 0)
			if sendMessage(a.workerSelect, testCBGetDroppedState, 0, 0) == 0 {
				t.Fatal("native dropdown did not open")
			}
			sendMessage(a.workerSelect, CB_SETCURSEL, uintptr(63-minDownloadWorkers), 0)
			// Native key handling emits the real parent notifications.
			sendMessage(a.workerSelect, 0x0100, 0x28, 0) // WM_KEYDOWN / VK_DOWN: preview 64
			if a.selectedDownloadWorkers() != 64 {
				t.Fatal("native keyboard preview failed")
			}
			// Even unrelated path synchronization must not commit a preview.
			a.syncPathsFromUI()
			cfg, err := loadConfig()
			if err != nil || cfg.DownloadWorkers != 16 || a.config.DownloadWorkers != 16 {
				t.Fatalf("unconfirmed selection persisted: memory=%d disk=%d err=%v", a.config.DownloadWorkers, cfg.DownloadWorkers, err)
			}
			key := uintptr(0x1B) // Escape
			want := 16
			if commit {
				key = 0x0D // Enter
				want = 64
			}
			sendMessage(a.workerSelect, 0x0100, key, 0)
			drainReviewWorkerMessages(a)
			if sendMessage(a.workerSelect, testCBGetDroppedState, 0, 0) != 0 {
				t.Fatal("native keyboard did not close dropdown")
			}
			cfg, err = loadConfig()
			if trace, ok := reviewWorkerEvents.Load(a.hwnd); ok {
				t.Logf("native events: %v", *trace.(*[]string))
			}
			if err != nil || cfg.DownloadWorkers != want || a.selectedDownloadWorkers() != want {
				t.Fatalf("commit=%v expected=%d disk=%d UI=%d err=%v", commit, want, cfg.DownloadWorkers, a.selectedDownloadWorkers(), err)
			}
			t.Logf("commit=%v persisted=%d selected=%d", commit, cfg.DownloadWorkers, a.selectedDownloadWorkers())
		})
	}
}

func reviewWindowRect(t *testing.T, hwnd HWND) RECT {
	t.Helper()
	var rect RECT
	ret, _, err := user32.NewProc("GetWindowRect").Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rect)))
	if ret == 0 {
		t.Fatal(err)
	}
	return rect
}

func TestReviewWorkerSmallWindowViewport(t *testing.T) {
	a := reviewWorkerApp(t, 12)
	for _, height := range []int32{200, 360, 400, 480, 760} {
		setWindowPos(a.hwnd, 0, 0, 980, height, SWP_NOZORDER|SWP_NOACTIVATE)
		a.layout()
		progress := reviewWindowRect(t, a.progressBar)
		log := reviewWindowRect(t, a.logEdit)
		available := max(int32(0), log.Top-12-(progress.Bottom+12))
		want := max(int32(0), int32(len(a.rows))*36-8-available)
		if available == 0 {
			want = 0
		}
		if got := a.maxScroll(); got != want {
			t.Errorf("height=%d viewport=%d maxScroll=%d want=%d", height, available, got, want)
		}
		info := SCROLLINFO{CbSize: uint32(unsafe.Sizeof(SCROLLINFO{})), FMask: SIF_ALL}
		getScrollInfo(a.hwnd, SB_VERT, &info)
		wantMaximum := int32(len(a.rows))*36 - 9
		if available == 0 {
			wantMaximum = 0
		}
		if info.NMax != wantMaximum {
			t.Errorf("scrollbar inclusive maximum=%d want=%d", info.NMax, wantMaximum)
		}
		for _, position := range []int32{0, want / 2, want} {
			a.setScrollPos(position)
			for _, row := range a.rows {
				style, _, _ := user32.NewProc("GetWindowLongPtrW").Call(uintptr(row.edit), ^uintptr(15))
				if style&WS_VISIBLE != 0 {
					r := reviewWindowRect(t, row.edit)
					if r.Top < progress.Bottom+12 || r.Bottom > log.Top-12 {
						t.Errorf("visible path input overlaps header/log at height=%d: row=%+v log=%+v", height, r, log)
					}
				}
			}
		}
	}
}

func TestReviewWorkerClosedKeyboardAndBusyPopup(t *testing.T) {
	preserveReviewConfig(t)
	a := reviewWorkerApp(t, 1)
	if err := saveConfig(a.config); err != nil {
		t.Fatal(err)
	}
	sendMessage(a.workerSelect, 0x0100, 0x28, 0) // Closed dropdown: next item commits immediately.
	cfg, err := loadConfig()
	if err != nil || cfg.DownloadWorkers != 17 || a.config.DownloadWorkers != 17 {
		t.Fatalf("closed keyboard change not saved: %+v %v", cfg, err)
	}
	sendMessage(a.workerSelect, testCBShowDropdown, 1, 0)
	sendMessage(a.workerSelect, 0x0100, 0x28, 0) // Preview 18, not committed.
	a.setUpdateBusy(true)
	drainReviewWorkerMessages(a)
	if sendMessage(a.workerSelect, testCBGetDroppedState, 0, 0) != 0 || a.selectedDownloadWorkers() != 17 || a.config.DownloadWorkers != 17 {
		t.Fatal("starting update committed preview or left popup open")
	}
	// A later path save must preserve the committed worker count.
	a.config.Paths[0] = "changed-path"
	a.scheduleConfigSave()
	if err := a.saveConfigNow(); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig()
	if err != nil || cfg.DownloadWorkers != 17 || cfg.Paths[0] != "changed-path" {
		t.Fatalf("path save lost committed selection: %+v %v", cfg, err)
	}
	a.setUpdateBusy(false)
	if a.selectedDownloadWorkers() != 17 {
		t.Fatal("unlock changed worker selection")
	}
}

func TestReviewWorkerNotificationOrdering(t *testing.T) {
	for _, sequence := range [][]uint16{
		{CBN_SELCHANGE, CBN_SELENDOK, CBN_CLOSEUP},
		{CBN_CLOSEUP, CBN_SELENDOK, CBN_SELCHANGE},
		{CBN_SELENDOK, CBN_CLOSEUP, CBN_SELCHANGE},
		{CBN_SELENDCANCEL, CBN_CLOSEUP, CBN_SELCHANGE},
		{CBN_CLOSEUP, CBN_SELCHANGE, CBN_SELENDCANCEL},
	} {
		t.Run(fmt.Sprint(sequence), func(t *testing.T) {
			preserveReviewConfig(t)
			a := reviewWorkerApp(t, 1)
			if err := saveConfig(a.config); err != nil {
				t.Fatal(err)
			}
			a.onDownloadWorkerSelection(CBN_DROPDOWN)
			sendMessage(a.workerSelect, CB_SETCURSEL, 61, 0)
			want := 16
			for _, code := range sequence {
				a.onDownloadWorkerSelection(code)
				if code == CBN_SELENDOK {
					want = 64
				}
			}
			drainReviewWorkerMessages(a)
			cfg, err := loadConfig()
			if err != nil || cfg.DownloadWorkers != want || a.config.DownloadWorkers != want || a.selectedDownloadWorkers() != want || a.workerDropdownActive || a.workerRestorePending {
				t.Fatalf("sequence=%v want=%d disk=%d memory=%d UI=%d active=%v pending=%v err=%v", sequence, want, cfg.DownloadWorkers, a.config.DownloadWorkers, a.selectedDownloadWorkers(), a.workerDropdownActive, a.workerRestorePending, err)
			}
		})
	}
}
