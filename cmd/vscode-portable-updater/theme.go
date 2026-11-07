package main

import (
	"fmt"
	"sync"
	"unsafe"
)

type appTheme struct {
	windowColor        uint32
	surfaceColor       uint32
	inputColor         uint32
	accentColor        uint32
	accentActiveColor  uint32
	borderColor        uint32
	textColor          uint32
	mutedTextColor     uint32
	windowBrush        HBRUSH
	surfaceBrush       HBRUSH
	inputBrush         HBRUSH
	accentBrush        HBRUSH
	accentActiveBrush  HBRUSH
	borderBrush        HBRUSH
	buttonSurfaceBrush HBRUSH
}

var (
	themeOnce sync.Once
	themeData appTheme
)

func rgb(r, g, b uint8) uint32 {
	return uint32(r) | uint32(g)<<8 | uint32(b)<<16
}

func ensureTheme() appTheme {
	themeOnce.Do(func() {
		themeData = appTheme{
			windowColor:        rgb(30, 30, 30),
			surfaceColor:       rgb(37, 37, 38),
			inputColor:         rgb(49, 49, 49),
			accentColor:        rgb(14, 99, 156),
			accentActiveColor:  rgb(17, 119, 187),
			borderColor:        rgb(63, 63, 70),
			textColor:          rgb(204, 204, 204),
			mutedTextColor:     rgb(156, 163, 175),
			windowBrush:        createSolidBrush(rgb(30, 30, 30)),
			surfaceBrush:       createSolidBrush(rgb(37, 37, 38)),
			inputBrush:         createSolidBrush(rgb(49, 49, 49)),
			accentBrush:        createSolidBrush(rgb(14, 99, 156)),
			accentActiveBrush:  createSolidBrush(rgb(17, 119, 187)),
			borderBrush:        createSolidBrush(rgb(63, 63, 70)),
			buttonSurfaceBrush: createSolidBrush(rgb(45, 45, 48)),
		}
	})
	return themeData
}

func appBackgroundBrush() HBRUSH {
	return ensureTheme().windowBrush
}

func (a *App) applyTheme() {
	theme := ensureTheme()
	applyVSCodeTitleBar(a.hwnd, theme)

	sendMessage(a.progressBar, PBM_SETRANGE32, 0, 100)
	sendMessage(a.progressBar, PBM_SETBKCOLOR, 0, uintptr(theme.surfaceColor))
	sendMessage(a.progressBar, PBM_SETBARCOLOR, 0, uintptr(theme.accentColor))

	a.setProgress("空闲", 0)
}

func applyVSCodeTitleBar(hwnd HWND, theme appTheme) {
	enabled := int32(1)
	_ = setDwmWindowAttribute(hwnd, DWMWA_USE_IMMERSIVE_DARK_MODE, unsafe.Pointer(&enabled), unsafe.Sizeof(enabled))
	_ = setDwmWindowAttribute(hwnd, DWMWA_CAPTION_COLOR, unsafe.Pointer(&theme.surfaceColor), unsafe.Sizeof(theme.surfaceColor))
	_ = setDwmWindowAttribute(hwnd, DWMWA_TEXT_COLOR, unsafe.Pointer(&theme.textColor), unsafe.Sizeof(theme.textColor))
	_ = setDwmWindowAttribute(hwnd, DWMWA_BORDER_COLOR, unsafe.Pointer(&theme.borderColor), unsafe.Sizeof(theme.borderColor))
}

func (a *App) handleCtlColor(msg uint32, hdc HDC, ctrl HWND) uintptr {
	theme := ensureTheme()

	switch ctrl {
	case a.workerHint:
		setTextColor(hdc, theme.mutedTextColor)
		setBkColor(hdc, theme.windowColor)
		setBkMode(hdc, TRANSPARENT)
		return uintptr(theme.windowBrush)
	case a.progressLabel:
		setTextColor(hdc, theme.textColor)
		setBkColor(hdc, theme.windowColor)
		setBkMode(hdc, TRANSPARENT)
		return uintptr(theme.windowBrush)
	case a.logEdit:
		setTextColor(hdc, theme.textColor)
		setBkColor(hdc, theme.inputColor)
		setBkMode(hdc, OPAQUE)
		return uintptr(theme.inputBrush)
	default:
		if msg == WM_CTLCOLOREDIT || msg == WM_CTLCOLORLISTBOX {
			setTextColor(hdc, theme.textColor)
			setBkColor(hdc, theme.inputColor)
			setBkMode(hdc, OPAQUE)
			return uintptr(theme.inputBrush)
		}
	}

	setTextColor(hdc, theme.textColor)
	setBkColor(hdc, theme.windowColor)
	setBkMode(hdc, TRANSPARENT)
	return uintptr(theme.windowBrush)
}

func (a *App) handleDrawItem(lParam uintptr) bool {
	if lParam == 0 {
		return false
	}

	var dis *DRAWITEMSTRUCT
	*(*uintptr)(unsafe.Pointer(&dis)) = lParam
	if dis.CtlType != ODT_BUTTON {
		return false
	}

	theme := ensureTheme()
	rect := dis.RcItem
	textRect := rect
	textRect.Left += 12
	textRect.Right -= 12

	fillBrush := theme.accentBrush
	textColor := theme.textColor
	if dis.ItemState&ODS_DISABLED != 0 {
		fillBrush = theme.buttonSurfaceBrush
		textColor = theme.mutedTextColor
	} else if dis.ItemState&ODS_SELECTED != 0 {
		fillBrush = theme.accentActiveBrush
	}

	if dis.CtlID >= rowDeleteIDBase && dis.CtlID < rowDeleteIDBase+10000 {
		fillBrush = theme.buttonSurfaceBrush
		if dis.ItemState&ODS_SELECTED != 0 {
			fillBrush = theme.surfaceBrush
		}
	}

	fillRect(dis.HDC, &rect, fillBrush)
	frameRect(dis.HDC, &rect, theme.borderBrush)

	setBkMode(dis.HDC, TRANSPARENT)
	setTextColor(dis.HDC, textColor)
	drawText(dis.HDC, getWindowText(dis.HwndItem), &textRect, DT_CENTER|DT_VCENTER|DT_SINGLELINE|DT_END_ELLIPSIS)
	return true
}

func (a *App) setProgress(label string, percent int) {
	if a.progressBar == 0 || a.progressLabel == 0 {
		return
	}

	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	setWindowText(a.progressLabel, fmt.Sprintf("%s %d%%", label, percent))
	sendMessage(a.progressBar, PBM_SETPOS, uintptr(percent), 0)
}
