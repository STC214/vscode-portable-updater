package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Paths []string `json:"paths"`
}

func cloneConfig(cfg Config) Config {
	out := Config{
		Paths: append([]string(nil), cfg.Paths...),
	}
	if len(out.Paths) == 0 {
		out.Paths = []string{""}
	}
	return out
}

func storageRoot() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}

	root := filepath.Dir(exePath)
	if isCDrivePath(root) {
		return "", errors.New("程序所在目录位于 C 盘，无法在不写入 C 盘的前提下保存配置和缓存，请将程序放到其他盘符后再运行")
	}

	return filepath.Join(root, "portable_vscode_updater_data"), nil
}

func ensureStorageDirs() (string, string, error) {
	root, err := storageRoot()
	if err != nil {
		return "", "", err
	}

	cacheDir := filepath.Join(root, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", "", err
	}

	return root, cacheDir, nil
}

func configPath() (string, error) {
	root, _, err := ensureStorageDirs()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "config.json"), nil
}

func loadConfig() (Config, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{Paths: []string{""}}, nil
		}
		return Config{}, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置失败: %w", err)
	}
	if len(cfg.Paths) == 0 {
		cfg.Paths = []string{""}
	}
	return cfg, nil
}

func saveConfig(cfg Config) error {
	if len(cfg.Paths) == 0 {
		cfg.Paths = []string{""}
	}

	path, err := configPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, path); err == nil {
		return nil
	}

	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return os.Rename(tmpPath, path)
	}

	backupPath := path + ".bak"
	_ = os.Remove(backupPath)
	if err := os.Rename(path, backupPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Rename(backupPath, path)
		_ = os.Remove(tmpPath)
		return err
	}

	_ = os.Remove(backupPath)
	return nil
}

func isCDrivePath(path string) bool {
	clean := filepath.Clean(path)
	vol := filepath.VolumeName(clean)
	return strings.EqualFold(vol, "C:")
}
