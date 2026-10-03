package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type updateTestTransport func(*http.Request) (*http.Response, error)

func (f updateTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func makeTestInstall(t *testing.T, root, version string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "resources", "app"), 0755); err != nil {
		t.Fatal(err)
	}
	// Minimal PE image: the updater only needs its machine architecture.
	image := make([]byte, 96)
	copy(image, "MZ")
	binary.LittleEndian.PutUint32(image[60:], 64)
	copy(image[64:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(image[68:], IMAGE_FILE_MACHINE_AMD64)
	if err := os.WriteFile(filepath.Join(root, "Code.exe"), image, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "resources", "app", "package.json"), []byte(fmt.Sprintf(`{"version":%q}`, version)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data", "settings.txt"), []byte("preserved"), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func testUpdateZIP(t *testing.T, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"Code.exe":                   "updated executable",
		"resources/app/package.json": fmt.Sprintf(`{"version":%q}`, version),
	} {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUpdateChecksVersionBeforeDownload(t *testing.T) {
	for _, tc := range []struct {
		name, local, metadata, zipVersion string
		status, installs, downloads       int
		wantError                         bool
	}{
		{"equal", "1.100.0", `{"productVersion":"1.100.0","version":"commit-id","url":"https://packages.test/update.zip"}`, "1.100.0", 200, 1, 0, false},
		{"newer", "1.101.0", `{"productVersion":"1.100.0","url":"https://packages.test/update.zip"}`, "1.100.0", 200, 1, 0, false},
		{"older", "1.99.0", `{"productVersion":"1.100.0","url":"https://packages.test/update.zip"}`, "1.100.0", 200, 1, 1, false},
		{"batch_cache", "1.99.0", `{"productVersion":"1.100.0","url":"https://packages.test/update.zip"}`, "1.100.0", 200, 2, 1, false},
		{"name_fallback", "1.100.0", `{"name":"1.100.0","version":"commit-id","url":"https://packages.test/update.zip"}`, "1.100.0", 200, 1, 0, false},
		{"api_error", "1.99.0", `{}`, "1.100.0", 503, 1, 0, true},
		{"invalid_json", "1.99.0", `{`, "1.100.0", 200, 1, 0, true},
		{"missing_version", "1.99.0", `{"version":"commit-id","url":"https://packages.test/update.zip"}`, "1.100.0", 200, 1, 0, true},
		{"invalid_version", "1.99.0", `{"productVersion":"bad.100.0","url":"https://packages.test/update.zip"}`, "1.100.0", 200, 1, 0, true},
		{"missing_url", "1.99.0", `{"productVersion":"1.100.0"}`, "1.100.0", 200, 1, 0, true},
		{"zip_mismatch", "1.99.0", `{"productVersion":"1.100.0","url":"https://packages.test/update.zip"}`, "1.101.0", 200, 1, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			var paths []string
			for i := 0; i < tc.installs; i++ {
				paths = append(paths, makeTestInstall(t, filepath.Join(base, fmt.Sprintf("install%d", i)), tc.local))
			}
			metadataCalls, downloads := 0, 0
			u := NewUpdater(filepath.Join(base, "cache"), func(string) {}, nil)
			u.client.Transport = updateTestTransport(func(r *http.Request) (*http.Response, error) {
				status, body := 200, ""
				if strings.HasPrefix(r.URL.Path, "/api/update/") {
					if r.URL.Path != "/api/update/win32-x64-archive/stable/latest" {
						t.Errorf("metadata path: %s", r.URL.Path)
					}
					metadataCalls++
					status, body = tc.status, tc.metadata
				} else {
					downloads++
					if metadataCalls == 0 {
						t.Errorf("ZIP requested before metadata")
					}
					body = string(testUpdateZIP(t, tc.zipVersion))
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Header: make(http.Header)}, nil
			})
			err := u.UpdateAll(paths)
			if (err != nil) != tc.wantError {
				t.Errorf("error = %v, wantError = %v", err, tc.wantError)
			}
			if metadataCalls != 1 || downloads != tc.downloads {
				t.Errorf("metadata=%d ZIP=%d; want 1/%d", metadataCalls, downloads, tc.downloads)
			}
			for _, root := range paths {
				version, err := readVersionFromFile(filepath.Join(root, "resources", "app", "package.json"))
				want := tc.local
				if tc.downloads > 0 && !tc.wantError {
					want = tc.zipVersion
				}
				if err != nil || version != want {
					t.Errorf("installed version=%s err=%v; want %s", version, err, want)
				}
				data, err := os.ReadFile(filepath.Join(root, "data", "settings.txt"))
				if err != nil || string(data) != "preserved" {
					t.Errorf("data changed: %q %v", data, err)
				}
			}
			if tc.downloads == 0 {
				if _, err := os.Stat(u.cacheDir); !os.IsNotExist(err) {
					t.Errorf("unexpected cache directory: %v", err)
				}
			}
			if _, err := os.Stat(filepath.Join(u.cacheDir, "latest-x64.zip.download")); !os.IsNotExist(err) {
				t.Errorf("temporary download retained: %v", err)
			}
			t.Logf("metadata=%d ZIP=%d error=%v", metadataCalls, downloads, err)
		})
	}
}

func TestUpdateBatchRechecksVersion(t *testing.T) {
	base := t.TempDir()
	root := makeTestInstall(t, filepath.Join(base, "install"), "1.100.0")
	u := NewUpdater(filepath.Join(base, "cache"), func(string) {}, nil)
	calls := 0
	u.client.Transport = updateTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if !strings.HasPrefix(r.URL.Path, "/api/update/") {
			t.Errorf("unexpected ZIP request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"productVersion":"1.100.0","url":"https://packages.test/update.zip"}`))}, nil
	})
	for i := 0; i < 2; i++ {
		if err := u.UpdateAll([]string{root}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Errorf("metadata calls=%d; want 2", calls)
	}
}

func TestUpdateARM64(t *testing.T) {
	base := t.TempDir()
	root := makeTestInstall(t, filepath.Join(base, "install"), "1.100.0")
	imagePath := filepath.Join(root, "Code.exe")
	image, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint16(image[68:], IMAGE_FILE_MACHINE_ARM64)
	if err := os.WriteFile(imagePath, image, 0644); err != nil {
		t.Fatal(err)
	}
	u := NewUpdater(filepath.Join(base, "cache"), func(string) {}, nil)
	calls := 0
	u.client.Transport = updateTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/api/update/win32-arm64-archive/stable/latest" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"productVersion":"1.100.0","url":"https://packages.test/arm64.zip"}`))}, nil
	})
	if err := u.UpdateAll([]string{root}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("requests=%d; want 1", calls)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.99.0", "1.100.0", -1},
		{"1.100.0", "1.100.0", 0},
		{"1.101.0", "1.100.0", 1},
		{"1.100.1", "1.100.0", 1},
		{"1.100", "1.100.0", 0},
	} {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compare(%s, %s)=%d; want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
