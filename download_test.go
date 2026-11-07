package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownload16(t *testing.T) {
	for _, mode := range []string{"parallel", "ignored", "retry", "bad-range", "truncated", "changed", "no-validator"} {
		t.Run(mode, func(t *testing.T) {
			// Uneven size and non-repeating segment contents detect gaps, overlaps,
			// incorrect WriteAt offsets and dropped final bytes during assembly.
			body := make([]byte, 8*1024*1024+13)
			for i := range body {
				body[i] = byte(i*31 + i/7919)
			}
			var calls, active, peak atomic.Int32
			var attempts sync.Map
			gate := make(chan struct{})
			var once sync.Once
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rangeValue := r.Header.Get("Range")
				if mode == "ignored" || rangeValue == "" {
					w.Write(body)
					return
				}
				var start, end int64
				if _, err := fmt.Sscanf(rangeValue, "bytes=%d-%d", &start, &end); err != nil {
					w.WriteHeader(400)
					return
				}
				if mode != "no-validator" {
					w.Header().Set("ETag", `"fixture-v1"`)
				}
				if rangeValue != "bytes=0-0" {
					calls.Add(1)
					if r.Header.Get("If-Range") != `"fixture-v1"` {
						w.WriteHeader(412)
						return
					}
					if mode == "retry" {
						if _, loaded := attempts.LoadOrStore(rangeValue, true); !loaded {
							w.WriteHeader(503)
							return
						}
					}
					if mode == "changed" {
						w.Write(body)
						return
					}
					if mode == "parallel" {
						n := active.Add(1)
						defer active.Add(-1)
						for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
						}
						if n == 16 {
							once.Do(func() { close(gate) })
						}
						select {
						case <-gate:
						case <-time.After(5 * time.Second):
						case <-r.Context().Done():
							return
						}
					}
				}
				crStart := start
				if mode == "bad-range" && rangeValue != "bytes=0-0" {
					crStart++
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", crStart, end, len(body)))
				w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
				w.WriteHeader(http.StatusPartialContent)
				if mode == "truncated" && rangeValue != "bytes=0-0" {
					w.Write(body[start : start+1])
					return
				}
				w.Write(body[start : end+1])
			}))
			defer srv.Close()
			path := filepath.Join(t.TempDir(), "download.zip")
			last := -1
			err := downloadFile(srv.Client(), srv.URL, path, func(p int) {
				if p < last || p < 0 || p > 100 {
					t.Errorf("invalid progress %d after %d", p, last)
				}
				last = p
			})
			// A 200 after If-Range means restart with one complete representation.
			wantErr := mode == "bad-range" || mode == "truncated"
			if (err != nil) != wantErr {
				t.Fatalf("error=%v wantError=%v", err, wantErr)
			}
			if wantErr {
				if last == 100 {
					t.Fatal("failed download reported completion")
				}
				t.Logf("rejected invalid response: %v", err)
				return
			}
			got, e := os.ReadFile(path)
			if e != nil || !bytes.Equal(got, body) || last != 100 {
				t.Fatalf("content/progress mismatch: bytes=%d err=%v progress=%d", len(got), e, last)
			}
			if mode == "parallel" && (calls.Load() != 16 || peak.Load() != 16) {
				t.Fatalf("ranges=%d peak=%d; want 16/16", calls.Load(), peak.Load())
			}
			if mode == "retry" && calls.Load() != 32 {
				t.Fatalf("retry requests=%d; want 32", calls.Load())
			}
			t.Logf("mode=%s ranges=%d peak=%d bytes=%d exact_match=true progress=%d", mode, calls.Load(), peak.Load(), len(got), last)
		})
	}
}

// Explicit opt-in; never download or install external packages in ordinary tests.
func TestDownload16Official(t *testing.T) {
	url := os.Getenv("UPDATER_TEST_DOWNLOAD_URL")
	path := os.Getenv("UPDATER_TEST_DOWNLOAD_PATH")
	version := os.Getenv("UPDATER_TEST_DOWNLOAD_VERSION")
	if url == "" || path == "" || version == "" {
		t.Skip("set UPDATER_TEST_DOWNLOAD_URL, UPDATER_TEST_DOWNLOAD_PATH and UPDATER_TEST_DOWNLOAD_VERSION")
	}
	start := time.Now()
	err := downloadFile(&http.Client{Timeout: 30 * time.Minute}, url, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("download bytes=%d elapsed=%s speed_MiBs=%.3f", fi.Size(), time.Since(start), float64(fi.Size())/time.Since(start).Seconds()/1024/1024)
	if err := validatePackageArchive(path, LatestRelease{Arch: "x64", Platform: "win32-x64-archive", Version: version}); err != nil {
		t.Fatal(err)
	}
	t.Log("official archive CRC/version/architecture validation passed; no installation performed")
}
