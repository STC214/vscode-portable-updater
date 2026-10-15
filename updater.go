package main

import (
	"archive/zip"
	"context"
	"debug/pe"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const versionQueryTimeout = 30 * time.Second

type LatestPackage struct {
	Arch       string
	Platform   string
	Version    string
	ZipPath    string
	Downloaded time.Time
}

type LatestRelease struct {
	Arch     string
	Platform string
	Version  string
	URL      string
}

type ProgressUpdate struct {
	Label   string
	Percent int
}

type Updater struct {
	cacheDir    string
	client      *http.Client
	logf        func(string)
	progressf   func(ProgressUpdate)
	cache       map[string]LatestPackage
	releases    map[string]LatestRelease
	rename      func(string, string) error // injectable filesystem failure tests
	checkClosed func(string) error
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
	if logf == nil {
		logf = func(string) {}
	}
	return &Updater{
		cacheDir:    cacheDir,
		rename:      os.Rename,
		checkClosed: ensureInstallClosed,
		client: &http.Client{
			Timeout: 30 * time.Minute,
		},
		logf:      logf,
		progressf: progressf,
		cache:     make(map[string]LatestPackage),
		releases:  make(map[string]LatestRelease),
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
	// Recheck the official release on each run, while sharing results within a batch.
	u.releases = make(map[string]LatestRelease)
	u.cache = make(map[string]LatestPackage)
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

	if err := pendingInstallTransaction(root); err != nil {
		return err
	}
	if err := validateInstallTarget(root, u.cacheDir); err != nil {
		return err
	}
	info, err := inspectInstall(root)
	if err != nil {
		return err
	}

	if !strings.EqualFold(info.AppRoot, info.Root) {
		u.logf(fmt.Sprintf("[%s] 检测到版本目录布局，实际应用目录: %s", info.Root, info.AppRoot))
	}
	u.logf(fmt.Sprintf("[%s] 本地版本 %s，架构 %s", info.Root, info.Version, info.Arch))

	queryStart := time.Now()
	release, err := u.getLatestRelease(info.Arch)
	u.logf(fmt.Sprintf("版本查询耗时 %s", time.Since(queryStart).Round(time.Millisecond)))
	if err != nil {
		return err
	}

	u.logf(fmt.Sprintf("[%s] 官网最新版本 %s", info.Root, release.Version))

	if compareVersions(info.Version, release.Version) >= 0 {
		u.reportProgress("已是最新版本", 100)
		u.logf(fmt.Sprintf("[%s] 已是最新版本，跳过。", info.Root))
		return nil
	}

	if err := u.checkClosed(info.Root); err != nil {
		return err
	}
	latest, err := u.getLatestPackage(release)
	if err != nil {
		return err
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

func (u *Updater) getLatestRelease(arch string) (LatestRelease, error) {
	if cached, ok := u.releases[arch]; ok {
		return cached, nil
	}

	var platform string
	switch arch {
	case "x64":
		platform = "win32-x64-archive"
	case "arm64":
		platform = "win32-arm64-archive"
	default:
		return LatestRelease{}, fmt.Errorf("不支持的架构: %s", arch)
	}

	endpoint := fmt.Sprintf("https://update.code.visualstudio.com/api/update/%s/stable/latest", platform)
	u.logf(fmt.Sprintf("检查官网最新版本: %s", endpoint))
	u.reportProgress("正在检查最新版本", 0)
	ctx, cancel := context.WithTimeout(context.Background(), versionQueryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return LatestRelease{}, err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return LatestRelease{}, fmt.Errorf("查询官网版本失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return LatestRelease{}, fmt.Errorf("查询官网版本失败: HTTP %d", resp.StatusCode)
	}
	var metadata struct {
		ProductVersion string `json:"productVersion"`
		Name           string `json:"name"`
		URL            string `json:"url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&metadata); err != nil {
		return LatestRelease{}, fmt.Errorf("解析官网版本失败: %w", err)
	}
	version := strings.TrimSpace(metadata.ProductVersion)
	if version == "" {
		version = strings.TrimSpace(metadata.Name)
	}
	// The API's "version" field is a commit ID, not a comparable version number.
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return LatestRelease{}, fmt.Errorf("官网版本号格式错误: %q", version)
	}
	for _, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return LatestRelease{}, fmt.Errorf("官网版本号格式错误: %q", version)
		}
	}
	downloadURL, err := url.Parse(metadata.URL)
	if err != nil || downloadURL.Scheme != "https" || downloadURL.Host == "" {
		return LatestRelease{}, errors.New("官网返回的下载地址格式错误")
	}
	release := LatestRelease{Arch: arch, Platform: platform, Version: version, URL: metadata.URL}
	u.releases[arch] = release
	return release, nil
}

func (u *Updater) getLatestPackage(release LatestRelease) (LatestPackage, error) {
	arch := release.Arch
	if cached, ok := u.cache[arch]; ok && cached.Version == release.Version {
		if _, err := os.Stat(cached.ZipPath); err == nil {
			u.reportProgress("使用已下载缓存", 100)
			return cached, nil
		}
	}
	zipPath := filepath.Join(u.cacheDir, fmt.Sprintf("latest-%s.zip", arch))
	// Validate CRC, layout, version and architecture, not just package.json.
	cacheCheckStart := time.Now()
	if err := validatePackageArchive(zipPath, release); err == nil {
		u.logf(fmt.Sprintf("磁盘更新包完整校验耗时 %s", time.Since(cacheCheckStart).Round(time.Millisecond)))
		version := release.Version
		pkg := LatestPackage{Arch: arch, Platform: release.Platform, Version: version, ZipPath: zipPath}
		u.cache[arch] = pkg
		u.logf(fmt.Sprintf("复用磁盘更新包缓存: %s，版本 %s", zipPath, version))
		u.reportProgress("使用磁盘缓存", 100)
		return pkg, nil
	}
	tmpPath := zipPath + ".download"

	u.logf(fmt.Sprintf("开始下载 %s 版本 %s 便携包: %s", arch, release.Version, release.URL))
	u.reportProgress("准备下载更新包", 0)
	if err := os.MkdirAll(u.cacheDir, 0o755); err != nil {
		return LatestPackage{}, err
	}
	_ = os.Remove(tmpPath)
	defer os.Remove(tmpPath)

	downloadStart := time.Now()
	if err := downloadFile(u.client, release.URL, tmpPath, func(percent int) {
		u.reportProgress("下载更新包", percent)
	}); err != nil {
		return LatestPackage{}, fmt.Errorf("下载官方更新包失败: %w", err)
	}
	u.logf(fmt.Sprintf("下载耗时 %s", time.Since(downloadStart).Round(time.Millisecond)))
	u.reportProgress("校验更新包完整性", 100)
	validationStart := time.Now()
	if err := validatePackageArchive(tmpPath, release); err != nil {
		return LatestPackage{}, err
	}
	u.logf(fmt.Sprintf("更新包完整校验耗时 %s", time.Since(validationStart).Round(time.Millisecond)))
	version := release.Version
	if err := os.Remove(zipPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return LatestPackage{}, err
	}
	if err := os.Rename(tmpPath, zipPath); err != nil {
		return LatestPackage{}, err
	}

	pkg := LatestPackage{
		Arch:       arch,
		Platform:   release.Platform,
		Version:    version,
		ZipPath:    zipPath,
		Downloaded: time.Now(),
	}
	u.cache[arch] = pkg
	u.reportProgress("下载完成", 100)
	return pkg, nil
}

func downloadFile(client *http.Client, url, targetPath string, report func(int)) (resultErr error) {
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
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()

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
		name := strings.ToLower(strings.ReplaceAll(file.Name, `\`, "/"))
		if name == "resources/app/package.json" || (strings.Count(name, "/") == 3 && strings.HasSuffix(name, "/resources/app/package.json")) {
			rc, err := file.Open()
			if err != nil {
				return "", err
			}
			data, err := io.ReadAll(io.LimitReader(rc, 4*1024*1024+1))
			if err == nil && len(data) > 4*1024*1024 {
				err = errors.New("更新包 package.json 过大")
			}
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
	if err := pendingInstallTransaction(info.Root); err != nil {
		return err
	}
	if err := validateInstallTarget(info.Root, u.cacheDir); err != nil {
		return err
	}
	parent := filepath.Dir(info.Root)
	base := filepath.Base(info.Root)
	stagingDir, err := os.MkdirTemp(parent, base+".update_staging-")
	if err != nil {
		return err
	}
	// Never remove staging when it may contain the only copy of user data.
	cleanupStaging := true
	defer func() {
		if cleanupStaging {
			_ = os.RemoveAll(stagingDir)
		}
	}()
	backupDir := filepath.Join(parent, fmt.Sprintf("%s.backup_old-%d", base, time.Now().UnixNano()))

	u.reportProgress("解压更新包", 0)
	u.logf(fmt.Sprintf("[%s] 解压更新包到 %s", info.Root, stagingDir))
	start := time.Now()
	extractedRoot, err := extractZip(latest.ZipPath, stagingDir)
	if err != nil {
		return fmt.Errorf("解压更新包失败: %w", err)
	}
	u.logf(fmt.Sprintf("解压耗时 %s", time.Since(start).Round(time.Millisecond)))
	staged, err := inspectInstall(extractedRoot)
	if err != nil {
		return fmt.Errorf("更新包安装结构校验失败: %w", err)
	}
	if staged.Version != latest.Version || staged.Arch != info.Arch {
		return fmt.Errorf("更新包版本或架构不匹配: %s/%s，预期 %s/%s", staged.Version, staged.Arch, latest.Version, info.Arch)
	}
	oldData := filepath.Join(info.Root, "data")
	newData := filepath.Join(extractedRoot, "data")
	hasData := false
	if dataInfo, err := os.Lstat(oldData); err == nil {
		// Reparse points need explicit treatment; do not move or traverse their targets.
		if !dataInfo.IsDir() || dataInfo.Mode()&os.ModeSymlink != 0 {
			return errors.New("原安装的 data 必须是普通目录")
		}
		hasData = true
		if err := os.RemoveAll(newData); err != nil {
			return fmt.Errorf("清理更新包自带 data 失败: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查原 data 目录失败: %w", err)
	}

	if err := u.checkClosed(info.Root); err != nil {
		return err
	}
	current, err := inspectInstall(info.Root)
	if err != nil {
		return err
	}
	if current.Version != info.Version || current.Arch != info.Arch || !strings.EqualFold(current.AppRoot, info.AppRoot) {
		return errors.New("下载或解压期间原安装已变化，请重新检查更新")
	}
	journal, err := writeInstallTransaction(info.Root, backupDir, extractedRoot)
	if err != nil {
		return err
	}
	transactionComplete := false
	defer func() {
		if transactionComplete {
			if err := os.Remove(journal); err != nil {
				u.logf(fmt.Sprintf("清理事务记录失败: %v", err))
			}
		}
	}()
	start = time.Now()
	u.logf(fmt.Sprintf("目录切换准备：旧程序备份 %s；新版暂存 %s；data 使用同卷重命名迁移", backupDir, extractedRoot))
	u.reportProgress("迁移数据并切换目录", 0)
	cleanupStaging = false // after this point even panic must retain possible user data
	if err := u.rename(info.Root, backupDir); err != nil {
		cleanupStaging = true
		transactionComplete = true
		return fmt.Errorf("重命名原目录失败，请先关闭 VSCode: %w", err)
	}
	backupData := filepath.Join(backupDir, "data")
	dataMoved := false
	rollback := func(cause error) error {
		if dataMoved {
			if err := u.rename(newData, backupData); err != nil {
				cleanupStaging = false
				return errors.Join(cause, fmt.Errorf("数据回滚失败；数据保留于 %s，原程序保留于 %s: %w", newData, backupDir, err))
			}
			dataMoved = false
		}
		if err := u.rename(backupDir, info.Root); err != nil {
			return errors.Join(cause, fmt.Errorf("目录回滚失败，完整原安装保留于 %s: %w", backupDir, err))
		}
		cleanupStaging = true
		transactionComplete = true
		u.logf("切换失败，已恢复原安装和 data")
		return cause
	}
	if hasData {
		if err := u.rename(backupData, newData); err != nil {
			return rollback(fmt.Errorf("迁移 data 失败: %w", err))
		}
		dataMoved = true
	}
	if err := u.rename(extractedRoot, info.Root); err != nil {
		return rollback(fmt.Errorf("切换新版目录失败: %w", err))
	}
	cleanupStaging = true
	transactionComplete = true
	u.logf(fmt.Sprintf("数据迁移及目录切换耗时 %s（目录重命名，未复制 data）", time.Since(start).Round(time.Millisecond)))

	start = time.Now()
	u.reportProgress("清理旧程序文件", 100)
	// data has already moved out: cleanup only deletes the old application.
	if err := os.RemoveAll(backupDir); err != nil {
		u.logf(fmt.Sprintf("旧程序清理失败，保留于 %s: %v", backupDir, err))
	}
	u.logf(fmt.Sprintf("旧程序清理耗时 %s", time.Since(start).Round(time.Millisecond)))
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
		safeName, err := archiveName(file)
		if err != nil {
			return "", err
		}
		cleanName := filepath.FromSlash(safeName)
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
		if err := errors.Join(dst.Close(), src.Close()); err != nil {
			return "", err
		}
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
