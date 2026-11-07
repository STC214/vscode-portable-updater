package main

import (
	"fmt"
	"runtime"
	"unsafe"
)

func (a *App) initDownloadWorkerControls() error {
	var err error
	a.workerLabel, err = a.newLabel("下载线程（3-64）：", workerLabelID)
	if err != nil {
		return err
	}
	a.workerSelect, err = a.newControl("COMBOBOX", "", WS_CHILD|WS_VISIBLE|WS_TABSTOP|WS_VSCROLL|CBS_DROPDOWNLIST, workerSelectID)
	if err != nil {
		return err
	}
	for workers := minDownloadWorkers; workers <= maxDownloadWorkers; workers++ {
		label := mustUTF16Ptr(fmt.Sprint(workers))
		index := int(sendMessage(a.workerSelect, CB_ADDSTRING, 0, uintptr(unsafe.Pointer(label))))
		// sendMessage accepts uintptr, so retain the UTF-16 allocation explicitly
		// until the native control has copied its text.
		runtime.KeepAlive(label)
		if index < 0 {
			return fmt.Errorf("创建下载线程选项 %d 失败", workers)
		}
	}
	a.config.DownloadWorkers = normalizeDownloadWorkers(a.config.DownloadWorkers)
	sendMessage(a.workerSelect, CB_SETCURSEL, uintptr(a.config.DownloadWorkers-minDownloadWorkers), 0)
	return nil
}

func (a *App) selectedDownloadWorkers() int {
	if a.workerSelect != 0 {
		index := int(sendMessage(a.workerSelect, CB_GETCURSEL, 0, 0))
		if index >= 0 && index <= maxDownloadWorkers-minDownloadWorkers {
			return index + minDownloadWorkers
		}
	}
	return normalizeDownloadWorkers(a.config.DownloadWorkers)
}

func (a *App) setUpdateBusy(busy bool) {
	a.busy = busy
	if busy && a.workerSelect != 0 {
		sendMessage(a.workerSelect, CB_SHOWDROPDOWN, 0, 0)
	}
	a.restoreDownloadWorkerSelection()
	enableWindow(a.updateButton, !busy)
	enableWindow(a.workerSelect, !busy)
}

func (a *App) restoreDownloadWorkerSelection() {
	if a.workerSelect != 0 {
		sendMessage(a.workerSelect, CB_SETCURSEL, uintptr(normalizeDownloadWorkers(a.config.DownloadWorkers)-minDownloadWorkers), 0)
	}
}

func (a *App) onDownloadWorkerSelection(code uint16) {
	if code == CBN_DROPDOWN {
		a.workerDropdownActive = true
		return
	}
	if code == CBN_SELENDCANCEL || code == CBN_CLOSEUP {
		// Windows does not guarantee CLOSEUP/SELCHANGE ordering. Restore after
		// the native notification sequence, without erasing a later SELENDOK.
		a.deferDownloadWorkerRestore()
		return
	}
	if code != CBN_SELCHANGE && code != CBN_SELENDOK {
		return
	}
	if a.busy {
		a.restoreDownloadWorkerSelection()
		return
	}
	// Navigation in an open dropdown is only a preview. Escape must discard it;
	// commit on SELENDOK, or on keyboard/wheel changes while the popup is closed.
	if code == CBN_SELCHANGE && (a.workerDropdownActive || sendMessage(a.workerSelect, CB_GETDROPPEDSTATE, 0, 0) != 0) {
		return
	}
	a.config.DownloadWorkers = a.selectedDownloadWorkers()
	a.syncPathsFromUI()
	if err := a.saveConfigNow(); err != nil {
		a.appendLog(fmt.Sprintf("保存下载线程设置失败: %v", err))
	}
}

func (a *App) deferDownloadWorkerRestore() {
	a.workerDropdownActive = true
	if a.workerRestorePending {
		return
	}
	a.workerRestorePending = true
	a.invokeUI(func() {
		a.workerRestorePending = false
		a.workerDropdownActive = false
		a.restoreDownloadWorkerSelection()
	})
}
