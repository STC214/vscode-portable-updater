package main

import (
	"archive/zip"
	"debug/pe"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type LatestPackage struct {
	Arch       string
	Platform   string
	Version    string
	ZipPath    string
	Downloaded time.Time
}

type ProgressUpdate struct {
	Label   string
	Percent int
}

type Updater struct {
	cacheDir  string
	client    *http.Client
	logf      func(string)
	progressf func(ProgressUpdate)
	cache     map[string]LatestPackage
}

type localPackageJSON struct {
	Version string `json:"version"`
}

type InstallInfo struct {
	Root        string
	AppRoot     string
	CodeExe     string
	Version     string
	Arch        string
	Platform    string
	PackageJSON string
}

func NewUpdater(cacheDir string, logf func(string), progressf func(ProgressUpdate)) *Updater {
	return &Updater{
		cacheDir: cacheDir,
		client: &http.Client{
			Timeout: 30 * time.Minute,
		},
		logf:      logf,
		progressf: progressf,
		cache:     make(map[string]LatestPackage),
	}
}

func (u *Updater) reportProgress(label string, percent int) {
	if u.progressf == nil {
		return
	}
	u.progressf(ProgressUpdate{
		Label:   label,
		Percent: percent,
	})
}

func (u *Updater) UpdateAll(paths []string) error {
	var errs []string

	for i, rawPath := range paths {
		path := normalizeInputPath(rawPath)
		if path == "" {
			continue
		}
		u.reportProgress(fmt.Sprintf("检查目标 %d/%d", i+1, len(paths)), 0)
		if err := u.updateOne(path); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", path, err))
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, " | "))
	}
	return nil
}

func (u *Updater) updateOne(root string) error {
	if isCDrivePath(root) {
		return errors.New("目标路径位于 C 盘，按要求不会对其进行写入或更新")
	}

	info, err := inspectInstall(root)
	if err != nil {
		return err
	}

	if !strings.EqualFold(info.AppRoot, info.Root) {
		u.logf(fmt.Sprintf("[%s] 检测到版本目录布局，实际应用目录: %s", info.Root, info.AppRoot))
	}
	u.logf(fmt.Sprintf("[%s] 本地版本 %s，架构 %s", info.Root, info.Version, info.Arch))

	latest, err := u.getLatestPackage(info.Arch)
	if err != nil {
		return err
	}

	u.logf(fmt.Sprintf("[%s] 官网最新版本 %s", info.Root, latest.Version))

	if compareVersions(info.Version, latest.Version) >= 0 {
		u.reportProgress("已是最新版本", 100)
		u.logf(fmt.Sprintf("[%s] 已是最新版本，跳过。", info.Root))
		return nil
	}

	u.logf(fmt.Sprintf("[%s] 开始升级到 %s", info.Root, latest.Version))
	u.reportProgress("正在替换文件", 100)
	if err := u.replaceInstall(info, latest); err != nil {
		return err
	}

	u.logf(fmt.Sprintf("[%s] 升级完成。", info.Root))
	u.reportProgress("升级完成", 100)
	return nil
}

func inspectInstall(root string) (InstallInfo, error) {
	root = filepath.Clean(root)
	codeExe := filepath.Join(root, "Code.exe")
	if _, err := os.Stat(codeExe); err != nil {
		return InstallInfo{}, fmt.Errorf("未找到 Code.exe: %w", err)
	}

	appRoot, versionPath, err := locatePackageJSON(root)
	if err != nil {
		return InstallInfo{}, err
	}

	version, err := readVersionFromFile(versionPath)
	if err != nil {
		return InstallInfo{}, err
	}

	arch, platform, err := detectCodeArchitecture(codeExe)
	if err != nil {
		return InstallInfo{}, err
	}

	return InstallInfo{
		Root:        root,
		AppRoot:     appRoot,
		CodeExe:     codeExe,
		Version:     version,
		Arch:        arch,
		Platform:    platform,
		PackageJSON: versionPath,
	}, nil
}

func locatePackageJSON(root string) (string, string, error) {
	root = filepath.Clean(root)

	direct := filepath.Join(root, "resources", "app", "package.json")
	if _, err := os.Stat(direct); err == nil {
		return root, direct, nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return "", "", fmt.Errorf("读取安装目录失败: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		candidateRoot := filepath.Join(root, entry.Name())
		candidate := filepath.Join(candidateRoot, "resources", "app", "package.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidateRoot, candidate, nil
		}
	}

	return "", "", errors.New("未找到 resources\\app\\package.json，目录结构不是预期的 VSCode 安装布局")
}

func detectCodeArchitecture(codeExe string) (string, string, error) {
	f, err := pe.Open(codeExe)
	if err != nil {
		return "", "", fmt.Errorf("读取 Code.exe 架构失败: %w", err)
	}
	defer f.Close()

	switch f.FileHeader.Machine {
	case IMAGE_FILE_MACHINE_AMD64:
		return "x64", "win32-x64-archive", nil
	case IMAGE_FILE_MACHINE_ARM64:
		return "arm64", "win32-arm64-archive", nil
	default:
		return "", "", fmt.Errorf("不支持的 VSCode 架构: 0x%x", f.FileHeader.Machine)
	}
}

func readVersionFromFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取版本文件失败: %w", err)
	}

	var pkg localPackageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", fmt.Errorf("解析版本文件失败: %w", err)
	}
	if pkg.Version == "" {
		return "", errors.New("版本文件中未找到 version")
	}
	return pkg.Version, nil
}

func (u *Updater) getLatestPackage(arch string) (LatestPackage, error) {
	if cached, ok := u.cache[arch]; ok {
		u.reportProgress("使用已下载缓存", 100)
		return cached, nil
	}

	var platform string
	switch arch {
	case "x64":
		platform = "win32-x64-archive"
	case "arm64":
		platform = "win32-arm64-archive"
	default:
		return LatestPackage{}, fmt.Errorf("不支持的架构: %s", arch)
	}

	url := fmt.Sprintf("https://update.code.visualstudio.com/latest/%s/stable", platform)
	zipPath := filepath.Join(u.cacheDir, fmt.Sprintf("latest-%s.zip", arch))
	tmpPath := zipPath + ".download"

	u.logf(fmt.Sprintf("开始从官网检查并下载最新 %s 便携包: %s", arch, url))
	u.reportProgress("准备下载更新包", 0)
	if err := os.MkdirAll(u.cacheDir, 0o755); err != nil {
		return LatestPackage{}, err
	}
	_ = os.Remove(tmpPath)

	if err := downloadFile(u.client, url, tmpPath, func(percent int) {
		u.reportProgress("下载更新包", percent)
	}); err != nil {
		return LatestPackage{}, fmt.Errorf("下载官方更新包失败: %w", err)
	}
	_ = os.Remove(zipPath)
	if err := os.Rename(tmpPath, zipPath); err != nil {
		return LatestPackage{}, err
	}

	version, err := readVersionFromZip(zipPath)
	if err != nil {
		return LatestPackage{}, err
	}

	pkg := LatestPackage{
		Arch:       arch,
		Platform:   platform,
		Version:    version,
		ZipPath:    zipPath,
		Downloaded: time.Now(),
	}
	u.cache[arch] = pkg
	u.reportProgress("下载完成", 100)
	return pkg, nil
}

func downloadFile(client *http.Client, url, targetPath string, report func(int)) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	file, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer file.Close()

	total := resp.ContentLength
	written := int64(0)
	lastReported := -1
	buf := make([]byte, 256*1024)

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := file.Write(buf[:n]); err != nil {
				return err
			}
			written += int64(n)
			if report != nil && total > 0 {
				percent := int(written * 100 / total)
				if percent > 100 {
					percent = 100
				}
				if percent != lastReported {
					lastReported = percent
					report(percent)
				}
			}
		}

		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	if report != nil {
		report(100)
	}
	return nil
}

func readVersionFromZip(zipPath string) (string, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	for _, file := range reader.File {
		if strings.HasSuffix(strings.ToLower(file.Name), "resources/app/package.json") {
			rc, err := file.Open()
			if err != nil {
				return "", err
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return "", err
			}
			var pkg localPackageJSON
			if err := json.Unmarshal(data, &pkg); err != nil {
				return "", err
			}
			if pkg.Version == "" {
				return "", errors.New("zip 包中没有 version 字段")
			}
			return pkg.Version, nil
		}
	}

	return "", errors.New("zip 包中未找到 resources/app/package.json")
}

func (u *Updater) replaceInstall(info InstallInfo, latest LatestPackage) error {
	parent := filepath.Dir(info.Root)
	base := filepath.Base(info.Root)
	stagingDir, err := os.MkdirTemp(parent, base+".update_staging-")
	if err != nil {
		return err
	}
	backupDir := filepath.Join(parent, fmt.Sprintf("%s.backup_old-%d", base, time.Now().UnixNano()))

	u.logf(fmt.Sprintf("[%s] 解压更新包到 %s", info.Root, stagingDir))
	extractedRoot, err := extractZip(latest.ZipPath, stagingDir)
	if err != nil {
		_ = os.RemoveAll(stagingDir)
		return fmt.Errorf("解压更新包失败: %w", err)
	}

	if err := os.Rename(info.Root, backupDir); err != nil {
		_ = os.RemoveAll(stagingDir)
		return fmt.Errorf("无法重命名原目录，请先关闭正在运行的 VSCode: %w", err)
	}

	restoreOriginal := func() {
		_ = os.RemoveAll(info.Root)
		_ = os.Rename(backupDir, info.Root)
	}

	if err := os.Rename(extractedRoot, info.Root); err != nil {
		restoreOriginal()
		return fmt.Errorf("替换新版本目录失败: %w", err)
	}

	oldData := filepath.Join(backupDir, "data")
	newData := filepath.Join(info.Root, "data")
	if _, err := os.Stat(oldData); err == nil {
		if _, err := os.Stat(newData); errors.Is(err, os.ErrNotExist) {
			if err := os.Rename(oldData, newData); err != nil {
				if err := copyDir(oldData, newData); err != nil {
					restoreOriginal()
					return fmt.Errorf("迁移 data 目录失败: %w", err)
				}
			}
		}
	}

	if err := os.RemoveAll(backupDir); err != nil {
		u.logf(fmt.Sprintf("[%s] 清理旧目录失败，可稍后手动删除: %v", info.Root, err))
	}
	if err := os.RemoveAll(stagingDir); err != nil {
		u.logf(fmt.Sprintf("[%s] 清理临时目录失败，可稍后手动删除: %v", info.Root, err))
	}
	return nil
}

func extractZip(zipPath, targetDir string) (string, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	targetPrefix := strings.ToLower(filepath.Clean(targetDir) + string(os.PathSeparator))
	for _, file := range reader.File {
		cleanName := filepath.Clean(file.Name)
		fullPath := filepath.Join(targetDir, cleanName)
		fullLower := strings.ToLower(fullPath)
		if !strings.HasPrefix(fullLower, targetPrefix) && !strings.EqualFold(fullPath, filepath.Clean(targetDir)) {
			return "", fmt.Errorf("zip 中存在非法路径: %s", file.Name)
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(fullPath, 0o755); err != nil {
				return "", err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return "", err
		}

		src, err := file.Open()
		if err != nil {
			return "", err
		}
		dst, err := os.Create(fullPath)
		if err != nil {
			src.Close()
			return "", err
		}
		if _, err := io.Copy(dst, src); err != nil {
			dst.Close()
			src.Close()
			return "", err
		}
		dst.Close()
		src.Close()
	}

	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return "", err
	}
	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(targetDir, entries[0].Name()), nil
	}

	codeExe := filepath.Join(targetDir, "Code.exe")
	if _, err := os.Stat(codeExe); err == nil {
		return targetDir, nil
	}

	return "", errors.New("解压后未找到 VSCode 根目录")
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}

		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()

		out, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
