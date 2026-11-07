package main

import (
	"fmt"
	"testing"
	"unsafe"
)

func TestToolbarFirstRowLayout(t *testing.T) {
	a := reviewWorkerApp(t, 3)
	for _, width := range []int32{600, 800, 980, 1400} {
		setWindowPos(a.hwnd, 0, 0, width, 760, SWP_NOZORDER|SWP_NOACTIVATE)
		a.layout()
		button := reviewWindowRect(t, a.updateButton)
		label := reviewWindowRect(t, a.workerLabel)
		combo := reviewWindowRect(t, a.workerSelect)
		hint := reviewWindowRect(t, a.workerHint)
		status := reviewWindowRect(t, a.progressLabel)
		bar := reviewWindowRect(t, a.progressBar)
		if label.Left < button.Right+16 || combo.Left < label.Right+8 || hint.Left < combo.Right+8 {
			t.Fatalf("toolbar groups overlap at width%d: button=%+v label=%+v combo=%+v hint=%+v", width, button, label, combo, hint)
		}
		center := (button.Top + button.Bottom) / 2
		for _, rect := range []RECT{label, combo, hint} {
			delta := (rect.Top+rect.Bottom)/2 - center
			if delta < -4 || delta > 4 {
				t.Fatalf("toolbar controls not centered at width%d: button=%+v control=%+v", width, button, rect)
			}
		}
		client := getClientRect(a.hwnd)
		if client.Right >= 820 {
			if status.Left < hint.Right+12 || status.Top < label.Top-2 || status.Top > label.Top+2 {
				t.Fatalf("wide status overlaps/misaligned: %+v", status)
			}
		} else if status.Top < button.Bottom+4 || status.Left < bar.Right+8 {
			t.Fatalf("narrow status overlaps toolbar/bar: status=%+v button=%+v bar=%+v", status, button, bar)
		}
		if bar.Bottom-bar.Top != 12 || bar.Top < button.Bottom+10 {
			t.Fatal("progress bar spacing/height incorrect")
		}
		if len(a.rows) > 0 && reviewWindowRect(t, a.rows[0].edit).Top != bar.Bottom+12 {
			t.Fatal("path area did not reclaim the removed settings row")
		}
		if a.config.DownloadWorkers != 16 || getWindowText(a.workerLabel) != "下载线程" || getWindowText(a.workerHint) != "3–64" {
			t.Fatal("layout changed settings or captions")
		}
		t.Logf("width=%d client=%d first-row aligned; narrow=%v", width, client.Right, client.Right < 820)
	}
}

func TestToolbarScrollbarResizeReentry(t *testing.T) {
	a := reviewWorkerApp(t, 1)
	reviewWorkerResizeHosts.Store(a.hwnd, true)
	for _, width := range []int32{600, 850, 980} {
		setWindowPos(a.hwnd, 0, 0, width, 760, SWP_NOZORDER|SWP_NOACTIVATE)
		for _, rows := range []int{1, 40, 1, 40} {
			a.config.Paths = make([]string, rows)
			for i := range a.config.Paths {
				a.config.Paths[i] = fmt.Sprintf("path%d", i)
			}
			a.rebuildPathRows()
			a.layout()
			log := reviewWindowRect(t, a.logEdit)
			remove := reviewWindowRect(t, a.rows[0].remove)
			if remove.Right != log.Right {
				t.Errorf("WM_SIZE reentry left stale row geometry: width=%d rows=%d delete.Right=%d log.Right=%d", width, rows, remove.Right, log.Right)
			}
			status := reviewWindowRect(t, a.progressLabel)
			client := getClientRect(a.hwnd)
			if client.Right < 820 && status.Top < reviewWindowRect(t, a.updateButton).Bottom+4 {
				t.Error("status did not reflow after scrollbar changed client width")
			}
			if a.layingOut || a.layoutPending {
				t.Fatal("layout did not finish stabilizing")
			}
		}
	}
}

func TestToolbarMinimumWindow(t *testing.T) {
	a := &App{}
	for _, initial := range []POINT{{X: 0, Y: 0}, {X: 800, Y: 500}} {
		info := MINMAXINFO{MinTrackSize: initial, MaxSize: POINT{X: 3000, Y: 2000}}
		a.WndProc(0, WM_GETMINMAXINFO, 0, uintptr(unsafe.Pointer(&info)))
		if info.MinTrackSize.X != max(initial.X, 600) || info.MinTrackSize.Y != max(initial.Y, 320) || info.MaxSize.X != 3000 || info.MaxSize.Y != 2000 {
			t.Fatalf("min/max constraints corrupted: %+v", info)
		}
	}
	a.WndProc(0, WM_GETMINMAXINFO, 0, 0)
}
