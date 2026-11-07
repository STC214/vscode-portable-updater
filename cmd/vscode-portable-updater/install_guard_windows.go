package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var queryProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")

func ensureInstallClosed(root string) error {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(snapshot)
	var entry syscall.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	err = syscall.Process32First(snapshot, &entry)
	for err == nil {
		h, e := syscall.OpenProcess(0x1000, false, entry.ProcessID)
		if e == nil {
			buf := make([]uint16, 32768)
			size := uint32(len(buf))
			ok, _, _ := queryProcessImageName.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
			syscall.CloseHandle(h)
			if ok != 0 {
				image := syscall.UTF16ToString(buf[:size])
				if pathWithin(root, image) {
					return fmt.Errorf("请先关闭目标目录内程序（PID %d）: %s", entry.ProcessID, image)
				}
			}
		}
		err = syscall.Process32Next(snapshot, &entry)
	}
	if !errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
		return err
	}
	return nil
}
func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}
func validateInstallTarget(root, cache string) error {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if isCDrivePath(resolved) {
		return errors.New("解析后的目标路径位于 C 盘")
	}
	// Reject a target reached through a junction, including ancestors.
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Clean(abs), filepath.Clean(resolved)) {
		return errors.New("目标路径包含目录链接，请使用真实安装路径")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if pathWithin(root, exe) || pathWithin(root, cache) {
		return errors.New("目标目录包含更新器或其缓存，请分开放置")
	}
	return nil
}
