package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func corruptPayloadZIP(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, e := w.CreateHeader(&zip.FileHeader{Name: "Code.exe", Method: zip.Store})
	if e != nil {
		t.Fatal(e)
	}
	f.Write(testPEImage())
	f, e = w.Create("resources/app/package.json")
	if e != nil {
		t.Fatal(e)
	}
	io.WriteString(f, `{"version":"1.100.0"}`)
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	data := b.Bytes()
	i := bytes.Index(data, testPEImage())
	if i < 0 {
		t.Fatal("payload not found")
	}
	data[i+90] ^= 1
	return data
}
func TestReviewCorruptPayloadCache(t *testing.T) {
	base := t.TempDir()
	cache := filepath.Join(base, "cache")
	os.MkdirAll(cache, 0755)
	os.WriteFile(filepath.Join(cache, "latest-x64.zip"), corruptPayloadZIP(t), 0600)
	downloads := 0
	u := NewUpdater(cache, func(s string) { t.Log(s) }, nil)
	u.client.Transport = updateTestTransport(func(r *http.Request) (*http.Response, error) {
		downloads++
		body := testUpdateZIP(t, "1.100.0")
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}, nil
	})
	_, e := u.getLatestPackage(LatestRelease{Arch: "x64", Version: "1.100.0", URL: "https://packages.test/update.zip"})
	if e != nil {
		t.Fatal(e)
	}
	if downloads != 1 {
		t.Fatalf("corrupt payload reused: downloads=%d want=1", downloads)
	}
}

func TestReviewPanicRetainsData(t *testing.T) {
	base := t.TempDir()
	root := makeTestInstall(t, filepath.Join(base, "install"), "1.99.0")
	zipPath := filepath.Join(base, "update.zip")
	os.WriteFile(zipPath, testUpdateZIP(t, "1.100.0"), 0600)
	info, e := inspectInstall(root)
	if e != nil {
		t.Fatal(e)
	}
	u := NewUpdater(filepath.Join(base, "cache"), func(s string) { t.Log(s) }, nil)
	calls := 0
	u.rename = func(a, b string) error {
		calls++
		if calls == 3 {
			panic("injected interruption after data move")
		}
		return os.Rename(a, b)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected panic")
			}
		}()
		u.replaceInstall(info, LatestPackage{Version: "1.100.0", ZipPath: zipPath})
	}()
	found := false
	filepath.Walk(base, func(p string, f os.FileInfo, e error) error {
		if e == nil && f.Name() == "settings.txt" {
			b, e := os.ReadFile(p)
			found = e == nil && string(b) == "preserved"
			t.Log("retained", p)
		}
		return e
	})
	if !found {
		t.Fatal("unique data deleted during panic cleanup")
	}
	if e := pendingInstallTransaction(root); e == nil {
		t.Fatal("interruption record missing")
	}
}

func TestReviewArchiveNames(t *testing.T) {
	for _, name := range []string{"../evil", "a/../../evil", `a\..\evil`, "/absolute", "C:/absolute", "a:stream", "NUL.txt", "com1", "a./b", "a /b", "a//b", "./Code.exe"} {
		f := &zip.File{FileHeader: zip.FileHeader{Name: name}}
		if _, err := archiveName(f); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
	for _, name := range []string{"Code.exe", "resources/app/package.json", "wrapper/resources/app/package.json", "dir/"} {
		f := &zip.File{FileHeader: zip.FileHeader{Name: name}}
		if _, err := archiveName(f); err != nil {
			t.Errorf("rejected %q: %v", name, err)
		}
	}
	f := &zip.File{FileHeader: zip.FileHeader{Name: "link"}}
	f.SetMode(os.ModeSymlink | 0777)
	if _, err := archiveName(f); err == nil {
		t.Fatal("symlink accepted")
	}
}
func TestReviewRunningProcessGuard(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Dir(exe)
	if e = ensureInstallClosed(root); e == nil {
		t.Fatal("own running process not detected")
	}
	if pathWithin(root+"-other", exe) {
		t.Fatal("prefix collision")
	}
	t.Log(e)
}
func TestReviewPendingTransaction(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "install")
	path, e := writeInstallTransaction(root, root+".backup", root+".staging")
	if e != nil {
		t.Fatal(e)
	}
	if _, e := writeInstallTransaction(root, "changed", "changed"); e == nil {
		t.Fatal("existing transaction overwritten")
	}
	u := NewUpdater(filepath.Join(base, "cache"), func(s string) { t.Log(s) }, nil)
	if e := u.UpdateAll([]string{root}); e == nil || !strings.Contains(e.Error(), path) {
		t.Fatalf("missing-root transaction not reported: %v", e)
	}
}
func TestReviewFinalProcessRecheck(t *testing.T) {
	base := t.TempDir()
	root := makeTestInstall(t, filepath.Join(base, "install"), "1.99.0")
	zipPath := filepath.Join(base, "update.zip")
	os.WriteFile(zipPath, testUpdateZIP(t, "1.100.0"), 0600)
	info, e := inspectInstall(root)
	if e != nil {
		t.Fatal(e)
	}
	u := NewUpdater(filepath.Join(base, "cache"), func(s string) { t.Log(s) }, nil)
	u.checkClosed = func(string) error { return errors.New("injected running process") }
	if e = u.replaceInstall(info, LatestPackage{Version: "1.100.0", ZipPath: zipPath}); e == nil {
		t.Fatal("running process ignored")
	}
	b, e := os.ReadFile(filepath.Join(root, "data", "settings.txt"))
	if e != nil || string(b) != "preserved" {
		t.Fatal("data changed")
	}
	if _, e = os.Stat(transactionPath(root)); !os.IsNotExist(e) {
		t.Fatal("transaction started before process check")
	}
}
func TestReviewNoDataInstall(t *testing.T) {
	base := t.TempDir()
	root := makeTestInstall(t, filepath.Join(base, "install"), "1.99.0")
	if e := os.Remove(filepath.Join(root, "data", "settings.txt")); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(filepath.Join(root, "data")); e != nil {
		t.Fatal(e)
	}
	zipPath := filepath.Join(base, "update.zip")
	os.WriteFile(zipPath, testUpdateZIP(t, "1.100.0"), 0600)
	info, e := inspectInstall(root)
	if e != nil {
		t.Fatal(e)
	}
	u := NewUpdater(filepath.Join(base, "cache"), func(s string) { t.Log(s) }, nil)
	if e = u.replaceInstall(info, LatestPackage{Version: "1.100.0", ZipPath: zipPath}); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(transactionPath(root)); !os.IsNotExist(e) {
		t.Fatal("completed transaction retained")
	}
}
func TestReviewRejectCacheInsideTarget(t *testing.T) {
	root := makeTestInstall(t, filepath.Join(t.TempDir(), "install"), "1.99.0")
	if e := validateInstallTarget(root, filepath.Join(root, "cache")); e == nil {
		t.Fatal("updater cache under installation accepted")
	}
}
func TestReviewRealArchive(t *testing.T) {
	path := os.Getenv("UPDATER_REAL_ARCHIVE")
	if path == "" {
		t.Skip("optional offline production ZIP validation")
	}
	version, e := readVersionFromZip(path)
	if e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	if e = validatePackageArchive(path, LatestRelease{Arch: "x64", Version: version}); e != nil {
		t.Fatal(e)
	}
	t.Logf("version=%s full CRC/architecture validation=%s", version, time.Since(start))
}

func TestReviewSourceChangedBeforeCommit(t *testing.T) {
	base := t.TempDir()
	root := makeTestInstall(t, filepath.Join(base, "install"), "1.99.0")
	info, e := inspectInstall(root)
	if e != nil {
		t.Fatal(e)
	}
	zipPath := filepath.Join(base, "update.zip")
	os.WriteFile(zipPath, testUpdateZIP(t, "1.100.0"), 0600)
	u := NewUpdater(filepath.Join(base, "cache"), nil, nil)
	u.checkClosed = func(string) error { return os.WriteFile(info.PackageJSON, []byte(`{"version":"1.101.0"}`), 0600) }
	if e = u.replaceInstall(info, LatestPackage{Version: "1.100.0", ZipPath: zipPath}); e == nil {
		t.Fatal("source change ignored")
	}
	v, e := readVersionFromFile(info.PackageJSON)
	if e != nil || v != "1.101.0" {
		t.Fatal("source overwritten")
	}
	if e = pendingInstallTransaction(root); e != nil {
		t.Fatal("transaction started despite source change")
	}
}
func TestReviewDuplicateZIPEntries(t *testing.T) {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for i := 0; i < 2; i++ {
		f, e := w.Create("Code.exe")
		if e != nil {
			t.Fatal(e)
		}
		f.Write(testPEImage())
	}
	f, e := w.Create("resources/app/package.json")
	if e != nil {
		t.Fatal(e)
	}
	io.WriteString(f, `{"version":"1.100.0"}`)
	w.Close()
	path := filepath.Join(t.TempDir(), "duplicate.zip")
	os.WriteFile(path, b.Bytes(), 0600)
	if e := validatePackageArchive(path, LatestRelease{Arch: "x64", Version: "1.100.0"}); e == nil {
		t.Fatal("duplicate entries accepted")
	}
}
func TestReviewJournalOnRollbackFailure(t *testing.T) {
	base := t.TempDir()
	root := makeTestInstall(t, filepath.Join(base, "install"), "1.99.0")
	info, e := inspectInstall(root)
	if e != nil {
		t.Fatal(e)
	}
	zipPath := filepath.Join(base, "update.zip")
	os.WriteFile(zipPath, testUpdateZIP(t, "1.100.0"), 0600)
	u := NewUpdater(filepath.Join(base, "cache"), nil, nil)
	calls := 0
	u.rename = func(a, b string) error {
		calls++
		if calls == 3 || calls == 4 {
			return errors.New("injected rollback failure")
		}
		return os.Rename(a, b)
	}
	if e = u.replaceInstall(info, LatestPackage{Version: "1.100.0", ZipPath: zipPath}); e == nil {
		t.Fatal("failure ignored")
	}
	if e = pendingInstallTransaction(root); e == nil {
		t.Fatal("rollback failure journal missing")
	}
	if e = u.UpdateAll([]string{root}); e == nil || !strings.Contains(e.Error(), transactionPath(root)) {
		t.Fatalf("retry not blocked by journal: %v", e)
	}
}
