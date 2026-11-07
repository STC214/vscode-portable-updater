package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerConfig(t *testing.T) {
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	before, beforeErr := os.ReadFile(path)
	t.Cleanup(func() {
		if beforeErr == nil {
			os.WriteFile(path, before, 0600)
		} else {
			os.Remove(path)
		}
	})
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{`{"paths":["legacy"]}`, 16},
		{`{"paths":["legacy"],"download_workers":0}`, 16},
		{`{"paths":["legacy"],"download_workers":-8}`, 3},
		{`{"paths":["legacy"],"download_workers":2}`, 3},
		{`{"paths":["legacy"],"download_workers":65}`, 64},
	} {
		if err := os.WriteFile(path, []byte(tc.raw), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := loadConfig()
		if err != nil || cfg.DownloadWorkers != tc.want || len(cfg.Paths) != 1 || cfg.Paths[0] != "legacy" {
			t.Fatalf("raw=%s cfg=%+v err=%v", tc.raw, cfg, err)
		}
	}
	for workers := 3; workers <= 64; workers++ {
		cfg := Config{Paths: []string{"preserved"}, DownloadWorkers: workers}
		clone := cloneConfig(cfg)
		clone.Paths[0] = "changed"
		if cfg.Paths[0] != "preserved" || clone.DownloadWorkers != workers {
			t.Fatal("clone lost workers or aliased paths")
		}
		if err := saveConfig(cfg); err != nil {
			t.Fatal(err)
		}
		loaded, err := loadConfig()
		if err != nil || loaded.DownloadWorkers != workers || loaded.Paths[0] != "preserved" {
			t.Fatalf("workers=%d loaded=%+v err=%v", workers, loaded, err)
		}
		updater := NewUpdaterWithWorkers(t.TempDir(), cfg.DownloadWorkers, nil, nil)
		cfg.DownloadWorkers = 3
		if updater.downloadWorkers != workers {
			t.Fatal("updater did not snapshot the worker setting")
		}
	}
	var persisted Config
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &persisted) != nil || persisted.DownloadWorkers != 64 {
		t.Fatal("saved setting missing")
	}
	t.Log("all 62 values persisted; legacy default=16, low/high values clamped=3/64")
}

func TestWorkerParallelism(t *testing.T) {
	data := make([]byte, 4*1024*1024+13)
	for i := range data {
		data[i] = byte(i*31 + i/7919)
	}
	for workers := 3; workers <= 64; workers++ {
		t.Run(fmt.Sprint(workers), func(t *testing.T) {
			var count, active, peak atomic.Int32
			gate := make(chan struct{})
			var once sync.Once
			client := &http.Client{Transport: updateTestTransport(func(r *http.Request) (*http.Response, error) {
				var start, end int64
				if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
					return nil, err
				}
				if r.Header.Get("Range") != "bytes=0-0" {
					count.Add(1)
					n := active.Add(1)
					defer active.Add(-1)
					for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
					}
					if n == int32(workers) {
						once.Do(func() { close(gate) })
					}
					select {
					case <-gate:
					case <-r.Context().Done():
						return nil, r.Context().Err()
					case <-time.After(5 * time.Second):
						return nil, context.DeadlineExceeded
					}
				}
				h := make(http.Header)
				h.Set("ETag", `"workers"`)
				h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
				return &http.Response{StatusCode: 206, Header: h, ContentLength: end - start + 1, Body: io.NopCloser(bytes.NewReader(data[start : end+1]))}, nil
			})}
			path := filepath.Join(t.TempDir(), "archive")
			if err := downloadFileWithWorkers(client, "https://fixture.test/workers", path, workers, nil); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, data) || count.Load() != int32(workers) || peak.Load() != int32(workers) {
				t.Fatalf("workers=%d requests=%d peak=%d bytes_match=%v err=%v", workers, count.Load(), peak.Load(), bytes.Equal(got, data), err)
			}
			t.Logf("selected=%d requests=%d peak=%d exact_match=true", workers, count.Load(), peak.Load())
		})
	}
}

func TestWorkerBoundaries(t *testing.T) {
	for workers := 3; workers <= 64; workers++ {
		for _, total := range []int64{4194317, math.MaxInt64} {
			previous := int64(0)
			for i := 0; i <= workers; i++ {
				next := downloadBoundary(total, i, workers)
				if next < previous || next > total {
					t.Fatalf("overflow total=%d workers=%d i=%d", total, workers, i)
				}
				previous = next
			}
			if previous != total {
				t.Fatal("last boundary is not total")
			}
		}
	}
}

func TestWorkerUpdaterIntegration(t *testing.T) {
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	entries := map[string][]byte{"Code.exe": testPEImage(), "resources/app/package.json": []byte(`{"version":"1.100.0"}`), "payload.bin": make([]byte, 4*1024*1024+13)}
	for name, body := range entries {
		entry, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data := archive.Bytes()
	for _, workers := range []int{3, 17, 64} {
		t.Run(fmt.Sprint(workers), func(t *testing.T) {
			var requests atomic.Int32
			var logs []string
			u := NewUpdaterWithWorkers(t.TempDir(), workers, func(s string) { logs = append(logs, s) }, nil)
			u.client.Transport = updateTestTransport(func(r *http.Request) (*http.Response, error) {
				var start, end int64
				if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
					return nil, err
				}
				if r.Header.Get("Range") != "bytes=0-0" {
					requests.Add(1)
				}
				h := make(http.Header)
				h.Set("ETag", `"archive"`)
				h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
				return &http.Response{StatusCode: 206, Header: h, ContentLength: end - start + 1, Body: io.NopCloser(bytes.NewReader(data[start : end+1]))}, nil
			})
			pkg, err := u.getLatestPackage(LatestRelease{Arch: "x64", Version: "1.100.0", URL: "https://fixture.test/valid.zip"})
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(pkg.ZipPath)
			if err != nil || !bytes.Equal(got, data) || requests.Load() != int32(workers) {
				t.Fatalf("workers=%d requests=%d archive_match=%v err=%v", workers, requests.Load(), bytes.Equal(got, data), err)
			}
			if !strings.Contains(strings.Join(logs, "\n"), fmt.Sprintf("下载策略：%d 路", workers)) {
				t.Fatalf("log missing actual worker setting: %v", logs)
			}
			t.Logf("updater workers=%d requests=%d valid_archive=true", workers, requests.Load())
		})
	}
}

func TestWorkerSelectorUI(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := initCommonControls(); err != nil {
		t.Fatal(err)
	}
	instance, err := getModuleHandle()
	if err != nil {
		t.Fatal(err)
	}
	parent, err := createWindowEx(0, "STATIC", "worker UI test", WS_OVERLAPPEDWINDOW, 0, 0, 980, 760, 0, 0, instance, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer destroyWindow(parent)
	a := &App{hwnd: parent, hInstance: instance, font: getDefaultGUIFont(), config: cloneConfig(Config{Paths: []string{"preserved"}})}
	if err := a.initUI(); err != nil {
		t.Fatal(err)
	}
	if count := sendMessage(a.workerSelect, CB_GETCOUNT, 0, 0); count != 62 {
		t.Fatalf("options=%d want62", count)
	}
	if a.selectedDownloadWorkers() != 16 {
		t.Fatal("legacy default missing from UI")
	}
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	before, beforeErr := os.ReadFile(path)
	t.Cleanup(func() {
		if beforeErr == nil {
			os.WriteFile(path, before, 0600)
		} else {
			os.Remove(path)
		}
	})
	for workers := 3; workers <= 64; workers++ {
		sendMessage(a.workerSelect, CB_SETCURSEL, uintptr(workers-3), 0)
		a.onCommand(uintptr(workerSelectID) | uintptr(CBN_SELCHANGE)<<16)
		cfg, err := loadConfig()
		if err != nil || cfg.DownloadWorkers != workers || getWindowText(a.workerSelect) != fmt.Sprint(workers) {
			t.Fatalf("selected=%d cfg=%+v err=%v", workers, cfg, err)
		}
	}
	a.setUpdateBusy(true)
	if enabled, _, _ := user32.NewProc("IsWindowEnabled").Call(uintptr(a.workerSelect)); enabled != 0 {
		t.Fatal("worker selector enabled during update")
	}
	sendMessage(a.workerSelect, CB_SETCURSEL, 0, 0)
	a.onCommand(uintptr(workerSelectID) | uintptr(CBN_SELCHANGE)<<16)
	if a.config.DownloadWorkers != 64 || a.selectedDownloadWorkers() != 64 {
		t.Fatal("busy notification changed active setting")
	}
	a.setUpdateBusy(false)
	if enabled, _, _ := user32.NewProc("IsWindowEnabled").Call(uintptr(a.workerSelect)); enabled == 0 {
		t.Fatal("worker selector not restored")
	}
	t.Log("native dropdown: 62 integers, legacy default16, selection saved, busy lock and restore passed")
}
