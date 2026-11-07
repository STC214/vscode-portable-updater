package main

import (
	"fmt"
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
		if int(sendMessage(a.workerSelect, CB_ADDSTRING, 0, uintptr(unsafe.Pointer(label)))) < 0 {
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
	enableWindow(a.updateButton, !busy)
	enableWindow(a.workerSelect, !busy)
}
