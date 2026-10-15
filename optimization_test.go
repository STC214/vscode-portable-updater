package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOptimizationDataIdentity(t *testing.T) {
	base := t.TempDir()
	root := makeTestInstall(t, filepath.Join(base, "install"), "1.99.0")
	before, _ := os.Stat(filepath.Join(root, "data", "settings.txt"))
	if !os.SameFile(before, before) {
		t.Fatal("preload original Windows file ID failed")
	}
	zipPath := filepath.Join(base, "update.zip")
	if err := os.WriteFile(zipPath, testUpdateZIP(t, "1.100.0"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := inspectInstall(root)
	if err != nil {
		t.Fatal(err)
	}
	u := NewUpdater(filepath.Join(base, "cache"), func(s string) { t.Log(s) }, nil)
	start := time.Now()
	if err := u.replaceInstall(info, LatestPackage{Version: "1.100.0", ZipPath: zipPath}); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(filepath.Join(root, "data", "settings.txt"))
	if err != nil {
		t.Fatal(err)
	}
	same := os.SameFile(before, after)
	t.Logf("sameFile=%v elapsed=%s", same, time.Since(start))
	if !same {
		t.Fatal("data was copied instead of renamed")
	}
}

func TestOptimizationRollback(t *testing.T) {
	for _, fail := range []int{1, 2, 3, 4, 5} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			base := t.TempDir()
			root := makeTestInstall(t, filepath.Join(base, "install"), "1.99.0")
			before, _ := os.Stat(filepath.Join(root, "data", "settings.txt"))
			if !os.SameFile(before, before) {
				t.Fatal("preload original Windows file ID failed")
			}
			zipPath := filepath.Join(base, "update.zip")
			if err := os.WriteFile(zipPath, testUpdateZIP(t, "1.100.0"), 0600); err != nil {
				t.Fatal(err)
			}
			info, err := inspectInstall(root)
			if err != nil {
				t.Fatal(err)
			}
			u := NewUpdater(filepath.Join(base, "cache"), func(s string) { t.Log(s) }, nil)
			calls := 0
			u.rename = func(a, b string) error {
				calls++
				// Cases 4/5 fail final installation and then data/directory rollback.
				if calls == fail || (fail >= 4 && calls == 3) {
					return errors.New("injected rename failure")
				}
				return os.Rename(a, b)
			}
			if err := u.replaceInstall(info, LatestPackage{Version: "1.100.0", ZipPath: zipPath}); err == nil {
				t.Fatal("expected failure")
			} else {
				t.Log(err)
			}
			if fail <= 3 {
				after, e := os.Stat(filepath.Join(root, "data", "settings.txt"))
				if e != nil || !os.SameFile(before, after) {
					t.Fatalf("data not restored: %v", e)
				}
				version, e := readVersionFromFile(info.PackageJSON)
				if e != nil || version != "1.99.0" {
					t.Fatalf("old installation not restored: %s %v", version, e)
				}
			} else {
				// Even a rollback failure must leave the original data intact somewhere.
				found := false
				err := filepath.Walk(base, func(p string, fi os.FileInfo, e error) error {
					if e != nil {
						return e
					}
					if fi.Name() == "settings.txt" && os.SameFile(before, fi) {
						b, e := os.ReadFile(p)
						found = e == nil && string(b) == "preserved"
						t.Logf("retained data: %s", p)
					}
					return nil
				})
				if err != nil || !found {
					t.Fatalf("data lost on rollback failure: %v", err)
				}
			}
		})
	}
}

func TestOptimizationDiskCache(t *testing.T) {
	for _, kind := range []string{"valid", "stale", "corrupt"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			cache := filepath.Join(base, "cache")
			if err := os.MkdirAll(cache, 0755); err != nil {
				t.Fatal(err)
			}
			archive := testUpdateZIP(t, "1.100.0")
			if kind == "stale" {
				archive = testUpdateZIP(t, "1.99.0")
			}
			if kind == "corrupt" {
				archive = []byte("broken")
			}
			if err := os.WriteFile(filepath.Join(cache, "latest-x64.zip"), archive, 0600); err != nil {
				t.Fatal(err)
			}
			downloads := 0
			for run := 0; run < 2; run++ {
				root := makeTestInstall(t, filepath.Join(base, fmt.Sprint(run)), "1.99.0")
				u := NewUpdater(cache, func(s string) { t.Log(s) }, nil)
				u.client.Transport = updateTestTransport(func(r *http.Request) (*http.Response, error) {
					body := `{"productVersion":"1.100.0","url":"https://packages.test/update.zip"}`
					if !strings.HasPrefix(r.URL.Path, "/api/update/") {
						downloads++
						body = string(testUpdateZIP(t, "1.100.0"))
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
				})
				if err := u.UpdateAll([]string{root}); err != nil {
					t.Fatal(err)
				}
			}
			want := 0
			if kind != "valid" {
				want = 1
			}
			if downloads != want {
				t.Fatalf("downloads=%d want=%d", downloads, want)
			}
			t.Logf("two fresh updater instances: downloads=%d", downloads)
		})
	}
}

func TestOptimizationPersistentLog(t *testing.T) {
	dir := t.TempDir()
	a := &App{storageDir: dir}
	a.appendLog("phase test")
	data, err := os.ReadFile(filepath.Join(dir, "logs", time.Now().Format("2006-01-02")+".log"))
	if err != nil || !strings.Contains(string(data), "phase test") || !strings.HasPrefix(string(data), time.Now().Format("2006-01-02")) {
		t.Fatalf("log=%q err=%v", data, err)
	}
	t.Logf("persisted: %s", data)
}
