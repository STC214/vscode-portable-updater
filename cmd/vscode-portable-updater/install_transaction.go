package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type installTransaction struct {
	Root    string
	Backup  string
	Staging string
}

func transactionPath(root string) string { return root + ".update-transaction.json" }
func pendingInstallTransaction(root string) error {
	path := transactionPath(root)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var record installTransaction
	if err := json.Unmarshal(data, &record); err != nil {
		return fmt.Errorf("检测到未完成事务记录 %s，请先核对备份和暂存目录: %w", path, err)
	}
	return fmt.Errorf("检测到未完成升级，已停止写入；请先恢复原安装后移走事务记录 %s；原目录=%s，备份=%s，暂存=%s", path, record.Root, record.Backup, record.Staging)
}
func writeInstallTransaction(root, backup, staging string) (string, error) {
	path := transactionPath(root)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(installTransaction{root, backup, staging})
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}
