package main

import (
	"archive/zip"
	"encoding/binary"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFixPackageValidationAndData(t *testing.T) {
	for _, name := range []string{"packaged_data", "wrapped_valid", "missing_exe", "invalid_exe", "wrong_arch", "wrong_version", "data_not_directory"} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			root := makeTestInstall(t, filepath.Join(base, "install"), "1.99.0")
			info, err := inspectInstall(root)
			if err != nil {
				t.Fatal(err)
			}
			image, err := os.ReadFile(info.CodeExe)
			if err != nil {
				t.Fatal(err)
			}
			files := map[string]string{"Code.exe": string(image), "resources/app/package.json": `{"version":"1.100.0"}`}
			switch name {
			case "packaged_data":
				files["data/default.txt"] = "package default"
			case "wrapped_valid", "missing_exe":
				files = map[string]string{"bundle/resources/app/package.json": `{"version":"1.100.0"}`}
				if name == "wrapped_valid" {
					files["bundle/Code.exe"] = string(image)
				}
			case "invalid_exe":
				files["Code.exe"] = "invalid PE"
			case "wrong_arch":
				binary.LittleEndian.PutUint16(image[68:], IMAGE_FILE_MACHINE_ARM64)
				files["Code.exe"] = string(image)
			case "wrong_version":
				files["resources/app/package.json"] = `{"version":"1.101.0"}`
			}
			zipPath := filepath.Join(base, "update.zip")
			f, err := os.Create(zipPath)
			if err != nil {
				t.Fatal(err)
			}
			w := zip.NewWriter(f)
			for path, body := range files {
				entry, err := w.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(entry, body); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if name == "data_not_directory" {
				if err := os.RemoveAll(filepath.Join(root, "data")); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "data"), []byte("original data file"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			u := NewUpdater(filepath.Join(base, "cache"), func(string) {}, nil)
			err = u.replaceInstall(info, LatestPackage{Version: "1.100.0", ZipPath: zipPath})
			wantError := name != "packaged_data" && name != "wrapped_valid"
			if (err != nil) != wantError {
				t.Fatalf("error=%v; wantError=%v", err, wantError)
			}
			version, e := readVersionFromFile(info.PackageJSON)
			wantVersion := "1.100.0"
			if wantError {
				wantVersion = "1.99.0"
			}
			if e != nil || version != wantVersion {
				t.Fatalf("version=%s error=%v", version, e)
			}
			if name != "data_not_directory" {
				data, e := os.ReadFile(filepath.Join(root, "data", "settings.txt"))
				if e != nil || string(data) != "preserved" {
					t.Fatalf("original data lost: %q %v", data, e)
				}
			} else {
				data, e := os.ReadFile(filepath.Join(root, "data"))
				if e != nil || string(data) != "original data file" {
					t.Fatalf("original data file changed: %q %v", data, e)
				}
			}
			if _, e := os.Stat(info.CodeExe); e != nil {
				t.Fatal(e)
			}
			staging, _ := filepath.Glob(root + ".update_staging-*")
			if len(staging) != 0 {
				t.Fatalf("staging left behind: %v", staging)
			}
			t.Logf("errorExpected=%v originalDataPreserved=true", wantError)
		})
	}
}

func TestFixVersionQueryDeadline(t *testing.T) {
	root := makeTestInstall(t, filepath.Join(t.TempDir(), "install"), "1.100.0")
	u := NewUpdater(filepath.Join(t.TempDir(), "cache"), func(string) {}, nil)
	u.client.Transport = updateTestTransport(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 31*time.Second {
			t.Errorf("query deadline=%v present=%v; want ~30s", deadline, ok)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"productVersion":"1.100.0","url":"https://packages.test/update.zip"}`))}, nil
	})
	if err := u.UpdateAll([]string{root}); err != nil {
		t.Fatal(err)
	}
}

func TestFixCloseWhileUpdating(t *testing.T) {
	if err := saveConfig(Config{Paths: []string{"sentinel"}}); err != nil {
		t.Fatal(err)
	}
	a := &App{busy: true, config: Config{Paths: []string{"should not save while busy"}}}
	a.WndProc(0, WM_CLOSE, 0, 0)
	cfg, err := loadConfig()
	if err != nil || len(cfg.Paths) != 1 || cfg.Paths[0] != "sentinel" {
		t.Fatalf("close handler proceeded while busy: %v %v", cfg, err)
	}
}

func TestFixConcurrentConfigSave(t *testing.T) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs <- saveConfig(Config{Paths: []string{strings.Repeat(string(rune('A'+i)), 128*1024)}})
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent save failed: %v", err)
		}
	}
	if _, err := loadConfig(); err != nil {
		t.Fatal(err)
	}
	a := &App{config: Config{Paths: []string{"old"}}}
	a.scheduleConfigSave()
	a.config = Config{Paths: []string{"new"}}
	if err := a.saveConfigNow(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	cfg, err := loadConfig()
	if err != nil || len(cfg.Paths) != 1 || cfg.Paths[0] != "new" {
		t.Fatalf("stale config persisted: %v %v", cfg, err)
	}
}
