package main

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Windows extraction paths must not include aliases, streams or reparse entries.
func archiveName(f *zip.File) (string, error) {
	name := strings.ReplaceAll(f.Name, `\`, "/")
	if strings.HasPrefix(name, "/") || strings.ContainsAny(name, ":\x00") || f.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("ZIP 路径或类型非法: %q", f.Name)
	}
	parts := strings.Split(strings.TrimSuffix(name, "/"), "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || strings.TrimRight(p, " .") != p {
			return "", fmt.Errorf("ZIP 路径非法: %q", f.Name)
		}
		stem := strings.ToUpper(strings.Split(p, ".")[0])
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9') {
			return "", fmt.Errorf("ZIP 使用保留文件名: %q", f.Name)
		}
	}
	return strings.Join(parts, "/"), nil
}

func validatePackageArchive(path string, release LatestRelease) error {
	version, err := readVersionFromZip(path)
	if err != nil {
		return err
	}
	if version != release.Version {
		return fmt.Errorf("更新包版本不匹配: %s，预期 %s", version, release.Version)
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer z.Close()
	seen := map[string]bool{}
	codeCount := 0
	for _, f := range z.File {
		name, err := archiveName(f)
		if err != nil {
			return err
		}
		key := strings.ToLower(name)
		if seen[key] {
			return fmt.Errorf("ZIP 路径重复: %s", name)
		}
		seen[key] = true
		if f.FileInfo().IsDir() {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		if strings.EqualFold(filepath.Base(name), "Code.exe") && strings.Count(name, "/") <= 1 {
			codeCount++
			header, e := io.ReadAll(io.LimitReader(r, 1024*1024))
			if e != nil {
				r.Close()
				return e
			}
			if len(header) < 64 || string(header[:2]) != "MZ" {
				r.Close()
				return errors.New("更新包 Code.exe 的 PE 头无效")
			}
			offset := uint64(binary.LittleEndian.Uint32(header[60:64]))
			if offset+6 > uint64(len(header)) || string(header[offset:offset+4]) != "PE\x00\x00" {
				r.Close()
				return errors.New("更新包 Code.exe 的 PE 签名无效")
			}
			machine := binary.LittleEndian.Uint16(header[offset+4 : offset+6])
			want := uint16(IMAGE_FILE_MACHINE_AMD64)
			if release.Arch == "arm64" {
				want = IMAGE_FILE_MACHINE_ARM64
			}
			if machine != want {
				r.Close()
				return fmt.Errorf("更新包架构不匹配: 0x%x", machine)
			}
		}
		_, copyErr := io.Copy(io.Discard, r)
		closeErr := r.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return fmt.Errorf("更新包文件校验失败 %s: %w", name, err)
		}
	}
	if codeCount != 1 {
		return fmt.Errorf("更新包应恰有一个主 Code.exe，实际 %d", codeCount)
	}
	return nil
}
