package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

type (
	HWND    uintptr
	HICON   uintptr
	HCURSOR uintptr
	HBRUSH  uintptr
	HFONT   uintptr
	HMENU   uintptr
	HMODULE uintptr
	HDC     uintptr
)

type WNDCLASSEX struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     HMODULE
	HIcon         HICON
	HCursor       HCURSOR
	HbrBackground HBRUSH
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       HICON
}

type POINT struct {
	X int32
	Y int32
}

type MSG struct {
	HWnd     HWND
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       POINT
	LPrivate uint32
}

type RECT struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type SCROLLINFO struct {
	CbSize    uint32
	FMask     uint32
	NMin      int32
	NMax      int32
	NPage     uint32
	NPos      int32
	NTrackPos int32
}

type DRAWITEMSTRUCT struct {
	CtlType    uint32
	CtlID      uint32
	ItemID     uint32
	ItemAction uint32
	ItemState  uint32
	HwndItem   HWND
	HDC        HDC
	RcItem     RECT
	ItemData   uintptr
}

type INITCOMMONCONTROLSEX struct {
	DwSize uint32
	DwICC  uint32
}

const (
	WS_OVERLAPPED       = 0x00000000
	WS_CAPTION          = 0x00C00000
	WS_SYSMENU          = 0x00080000
	WS_THICKFRAME       = 0x00040000
	WS_MINIMIZEBOX      = 0x00020000
	WS_MAXIMIZEBOX      = 0x00010000
	WS_VISIBLE          = 0x10000000
	WS_CHILD            = 0x40000000
	WS_VSCROLL          = 0x00200000
	WS_TABSTOP          = 0x00010000
	WS_BORDER           = 0x00800000
	WS_OVERLAPPEDWINDOW = WS_OVERLAPPED | WS_CAPTION | WS_SYSMENU | WS_THICKFRAME | WS_MINIMIZEBOX | WS_MAXIMIZEBOX

	ES_LEFT        = 0x0000
	ES_MULTILINE   = 0x0004
	ES_AUTOVSCROLL = 0x0040
	ES_READONLY    = 0x0800

	BS_OWNERDRAW     = 0x0000000B
	CBS_DROPDOWNLIST = 0x0003
	CB_ADDSTRING     = 0x0143
	CB_GETCOUNT      = 0x0146
	CB_GETCURSEL     = 0x0147
	CB_SETCURSEL     = 0x014E
	CBN_SELCHANGE    = 1

	PBS_SMOOTH = 0x01

	CW_USEDEFAULT int32 = -2147483648

	SW_HIDE        = 0
	SW_SHOW        = 5
	SW_RESTORE     = 9
	SWP_NOZORDER   = 0x0004
	SWP_NOACTIVATE = 0x0010

	WM_CREATE          = 0x0001
	WM_DESTROY         = 0x0002
	WM_SIZE            = 0x0005
	WM_CLOSE           = 0x0010
	WM_DRAWITEM        = 0x002B
	WM_COMMAND         = 0x0111
	WM_SETFONT         = 0x0030
	WM_VSCROLL         = 0x0115
	WM_CTLCOLORBTN     = 0x0135
	WM_CTLCOLORLISTBOX = 0x0134
	WM_CTLCOLOREDIT    = 0x0133
	WM_CTLCOLORSTATIC  = 0x0138
	WM_MOUSEWHEEL      = 0x020A
	WM_APP             = 0x8000
	WM_NCCREATE        = 0x0081

	EM_SETSEL     = 0x00B1
	EM_REPLACESEL = 0x00C2

	BN_CLICKED = 0
	EN_CHANGE  = 0x0300

	IDC_ARROW       = 32512
	IDI_APPLICATION = 32512
	MB_OK           = 0x00000000
	MB_ICONERROR    = 0x00000010

	SB_VERT          = 1
	SB_LINEUP        = 0
	SB_LINEDOWN      = 1
	SB_PAGEUP        = 2
	SB_PAGEDOWN      = 3
	SB_THUMBPOSITION = 4
	SB_THUMBTRACK    = 5
	SB_TOP           = 6
	SB_BOTTOM        = 7

	SIF_RANGE    = 0x0001
	SIF_PAGE     = 0x0002
	SIF_POS      = 0x0004
	SIF_TRACKPOS = 0x0010
	SIF_ALL      = SIF_RANGE | SIF_PAGE | SIF_POS | SIF_TRACKPOS

	DEFAULT_GUI_FONT = 17

	CS_HREDRAW = 0x0002
	CS_VREDRAW = 0x0001

	ICC_PROGRESS_CLASS = 0x00000020

	IMAGE_FILE_MACHINE_I386  = 0x014c
	IMAGE_FILE_MACHINE_AMD64 = 0x8664
	IMAGE_FILE_MACHINE_ARM64 = 0xaa64

	ODT_BUTTON   = 4
	ODS_SELECTED = 0x0001
	ODS_DISABLED = 0x0004

	DT_CENTER       = 0x00000001
	DT_VCENTER      = 0x00000004
	DT_SINGLELINE   = 0x00000020
	DT_END_ELLIPSIS = 0x00008000

	TRANSPARENT = 1
	OPAQUE      = 2

	WM_USER = 0x0400

	PBM_SETPOS      = WM_USER + 2
	PBM_SETRANGE32  = WM_USER + 6
	PBM_SETBARCOLOR = WM_USER + 9
	PBM_SETBKCOLOR  = 0x2001

	DWMWA_USE_IMMERSIVE_DARK_MODE = 20
	DWMWA_BORDER_COLOR            = 34
	DWMWA_CAPTION_COLOR           = 35
	DWMWA_TEXT_COLOR              = 36
)

var (
	user32                   = syscall.NewLazyDLL("user32.dll")
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	gdi32                    = syscall.NewLazyDLL("gdi32.dll")
	comctl32                 = syscall.NewLazyDLL("comctl32.dll")
	dwmapi                   = syscall.NewLazyDLL("dwmapi.dll")
	procCreateWindowExW      = user32.NewProc("CreateWindowExW")
	procDefWindowProcW       = user32.NewProc("DefWindowProcW")
	procDestroyWindow        = user32.NewProc("DestroyWindow")
	procDispatchMessageW     = user32.NewProc("DispatchMessageW")
	procDrawTextW            = user32.NewProc("DrawTextW")
	procEnableWindow         = user32.NewProc("EnableWindow")
	procFillRect             = user32.NewProc("FillRect")
	procFrameRect            = user32.NewProc("FrameRect")
	procGetClientRect        = user32.NewProc("GetClientRect")
	procGetMessageW          = user32.NewProc("GetMessageW")
	procGetScrollInfo        = user32.NewProc("GetScrollInfo")
	procGetWindowTextW       = user32.NewProc("GetWindowTextW")
	procGetWindowTextLenW    = user32.NewProc("GetWindowTextLengthW")
	procLoadCursorW          = user32.NewProc("LoadCursorW")
	procLoadIconW            = user32.NewProc("LoadIconW")
	procMessageBoxW          = user32.NewProc("MessageBoxW")
	procPostMessageW         = user32.NewProc("PostMessageW")
	procPostQuitMessage      = user32.NewProc("PostQuitMessage")
	procRegisterClassExW     = user32.NewProc("RegisterClassExW")
	procSendMessageW         = user32.NewProc("SendMessageW")
	procSetScrollInfo        = user32.NewProc("SetScrollInfo")
	procSetWindowPos         = user32.NewProc("SetWindowPos")
	procSetWindowTextW       = user32.NewProc("SetWindowTextW")
	procShowWindow           = user32.NewProc("ShowWindow")
	procTranslateMessage     = user32.NewProc("TranslateMessage")
	procUpdateWindow         = user32.NewProc("UpdateWindow")
	procGetModuleHandleW     = kernel32.NewProc("GetModuleHandleW")
	procCreateSolidBrush     = gdi32.NewProc("CreateSolidBrush")
	procDeleteObject         = gdi32.NewProc("DeleteObject")
	procGetStockObject       = gdi32.NewProc("GetStockObject")
	procSetBkColor           = gdi32.NewProc("SetBkColor")
	procSetBkMode            = gdi32.NewProc("SetBkMode")
	procSetTextColor         = gdi32.NewProc("SetTextColor")
	procInitCommonControlsEx = comctl32.NewProc("InitCommonControlsEx")
	procDwmSetWindowAttr     = dwmapi.NewProc("DwmSetWindowAttribute")
)

func mustUTF16Ptr(s string) *uint16 {
	ptr, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		panic(err)
	}
	return ptr
}

func getModuleHandle() (HMODULE, error) {
	ret, _, err := procGetModuleHandleW.Call(0)
	if ret == 0 {
		if err != syscall.Errno(0) {
			return 0, err
		}
		return 0, fmt.Errorf("GetModuleHandleW returned 0")
	}
	return HMODULE(ret), nil
}

func initCommonControls() error {
	icc := INITCOMMONCONTROLSEX{
		DwSize: uint32(unsafe.Sizeof(INITCOMMONCONTROLSEX{})),
		DwICC:  ICC_PROGRESS_CLASS,
	}
	ret, _, err := procInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&icc)))
	if ret == 0 {
		if err != syscall.Errno(0) {
			return err
		}
		return fmt.Errorf("InitCommonControlsEx returned 0")
	}
	return nil
}

func loadAppIcon(hInstance HMODULE) HICON {
	if ret, _, _ := procLoadIconW.Call(uintptr(hInstance), 1); ret != 0 {
		return HICON(ret)
	}
	ret, _, _ := procLoadIconW.Call(0, IDI_APPLICATION)
	return HICON(ret)
}

func registerClass(className string, wndProc uintptr, hInstance HMODULE) error {
	cursor, _, _ := procLoadCursorW.Call(0, IDC_ARROW)
	icon := loadAppIcon(hInstance)
	wc := WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(WNDCLASSEX{})),
		Style:         CS_HREDRAW | CS_VREDRAW,
		LpfnWndProc:   wndProc,
		HInstance:     hInstance,
		HIcon:         icon,
		HCursor:       HCURSOR(cursor),
		HbrBackground: appBackgroundBrush(),
		LpszClassName: mustUTF16Ptr(className),
		HIconSm:       icon,
	}
	ret, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if ret == 0 {
		return err
	}
	return nil
}

func createWindowEx(exStyle uint32, className, title string, style uint32, x, y, width, height int32, parent HWND, menu HMENU, instance HMODULE, param uintptr) (HWND, error) {
	ret, _, err := procCreateWindowExW.Call(
		uintptr(exStyle),
		uintptr(unsafe.Pointer(mustUTF16Ptr(className))),
		uintptr(unsafe.Pointer(mustUTF16Ptr(title))),
		uintptr(style),
		uintptr(x),
		uintptr(y),
		uintptr(width),
		uintptr(height),
		uintptr(parent),
		uintptr(menu),
		uintptr(instance),
		param,
	)
	if ret == 0 {
		return 0, err
	}
	return HWND(ret), nil
}

func defWindowProc(hwnd HWND, msg uint32, wParam, lParam uintptr) uintptr {
	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

func showWindow(hwnd HWND, cmd int32) {
	procShowWindow.Call(uintptr(hwnd), uintptr(cmd))
}

func updateWindow(hwnd HWND) {
	procUpdateWindow.Call(uintptr(hwnd))
}

func getMessage(msg *MSG) (int32, error) {
	ret, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(msg)), 0, 0, 0)
	if int32(ret) == -1 {
		return -1, err
	}
	return int32(ret), nil
}

func translateMessage(msg *MSG) {
	procTranslateMessage.Call(uintptr(unsafe.Pointer(msg)))
}

func dispatchMessage(msg *MSG) {
	procDispatchMessageW.Call(uintptr(unsafe.Pointer(msg)))
}

func postQuitMessage(code int32) {
	procPostQuitMessage.Call(uintptr(code))
}

func messageBox(hwnd HWND, text, caption string, flags uint32) {
	procMessageBoxW.Call(
		uintptr(hwnd),
		uintptr(unsafe.Pointer(mustUTF16Ptr(text))),
		uintptr(unsafe.Pointer(mustUTF16Ptr(caption))),
		uintptr(flags),
	)
}

func getDefaultGUIFont() HFONT {
	ret, _, _ := procGetStockObject.Call(DEFAULT_GUI_FONT)
	return HFONT(ret)
}

func sendMessage(hwnd HWND, msg uint32, wParam, lParam uintptr) uintptr {
	ret, _, _ := procSendMessageW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

func setWindowText(hwnd HWND, text string) {
	procSetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(mustUTF16Ptr(text))))
}

func getWindowText(hwnd HWND) string {
	length, _, _ := procGetWindowTextLenW.Call(uintptr(hwnd))
	buf := make([]uint16, length+1)
	procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func getClientRect(hwnd HWND) RECT {
	var rect RECT
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rect)))
	return rect
}

func setWindowPos(hwnd HWND, x, y, width, height int32, flags uint32) {
	procSetWindowPos.Call(uintptr(hwnd), 0, uintptr(x), uintptr(y), uintptr(width), uintptr(height), uintptr(flags))
}

func destroyWindow(hwnd HWND) {
	if hwnd == 0 {
		return
	}
	procDestroyWindow.Call(uintptr(hwnd))
}

func enableWindow(hwnd HWND, enable bool) {
	var v uintptr
	if enable {
		v = 1
	}
	procEnableWindow.Call(uintptr(hwnd), v)
}

func postMessage(hwnd HWND, msg uint32, wParam, lParam uintptr) {
	procPostMessageW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
}

func setScrollInfo(hwnd HWND, bar int32, info *SCROLLINFO, redraw bool) {
	var redrawFlag uintptr
	if redraw {
		redrawFlag = 1
	}
	procSetScrollInfo.Call(uintptr(hwnd), uintptr(bar), uintptr(unsafe.Pointer(info)), redrawFlag)
}

func getScrollInfo(hwnd HWND, bar int32, info *SCROLLINFO) {
	procGetScrollInfo.Call(uintptr(hwnd), uintptr(bar), uintptr(unsafe.Pointer(info)))
}

func createSolidBrush(color uint32) HBRUSH {
	ret, _, _ := procCreateSolidBrush.Call(uintptr(color))
	return HBRUSH(ret)
}

func deleteObject(obj uintptr) {
	if obj == 0 {
		return
	}
	procDeleteObject.Call(obj)
}

func setTextColor(hdc HDC, color uint32) {
	procSetTextColor.Call(uintptr(hdc), uintptr(color))
}

func setBkColor(hdc HDC, color uint32) {
	procSetBkColor.Call(uintptr(hdc), uintptr(color))
}

func setBkMode(hdc HDC, mode int32) {
	procSetBkMode.Call(uintptr(hdc), uintptr(mode))
}

func fillRect(hdc HDC, rect *RECT, brush HBRUSH) {
	procFillRect.Call(uintptr(hdc), uintptr(unsafe.Pointer(rect)), uintptr(brush))
}

func frameRect(hdc HDC, rect *RECT, brush HBRUSH) {
	procFrameRect.Call(uintptr(hdc), uintptr(unsafe.Pointer(rect)), uintptr(brush))
}

func drawText(hdc HDC, text string, rect *RECT, format uint32) {
	procDrawTextW.Call(
		uintptr(hdc),
		uintptr(unsafe.Pointer(mustUTF16Ptr(text))),
		uintptr(len([]rune(text))),
		uintptr(unsafe.Pointer(rect)),
		uintptr(format),
	)
}

func setDwmWindowAttribute(hwnd HWND, attr uint32, value unsafe.Pointer, size uintptr) error {
	ret, _, err := procDwmSetWindowAttr.Call(uintptr(hwnd), uintptr(attr), uintptr(value), size)
	if ret != 0 {
		if err != syscall.Errno(0) {
			return err
		}
		return fmt.Errorf("DwmSetWindowAttribute failed with HRESULT 0x%x", uint32(ret))
	}
	return nil
}

func showFatalError(text string) {
	messageBox(0, text, "AutoUpdate VSCode", MB_OK|MB_ICONERROR)
}

func lowWord(v uintptr) uint16 {
	return uint16(v & 0xffff)
}

func highWord(v uintptr) uint16 {
	return uint16((v >> 16) & 0xffff)
}
