//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// installBinary atomically replaces the running executable on Unix. A backup
// is required: if it cannot be created, the replacement is refused so a failed
// exec can always be rolled back.
func installBinary(tempPath, execPath string) (installed bool, backupPath, newPath string, err error) {
	backupPath, err = createExecutableBackup(execPath)
	if err != nil {
		return false, "", "", fmt.Errorf("创建更新备份失败，拒绝替换: %w", err)
	}
	if renameErr := os.Rename(tempPath, execPath); renameErr != nil {
		_ = os.Remove(backupPath)
		return false, "", "", fmt.Errorf("替换可执行文件失败: %w", renameErr)
	}
	return true, backupPath, execPath, nil
}

// restartProcess replaces the current process image with the new executable.
// On failure it restores the backup and reports whether the rollback succeeded.
func restartProcess(execPath, backupPath string) (rolledBack bool, err error) {
	argv := append([]string{execPath}, os.Args[1:]...)
	if execErr := syscall.Exec(execPath, argv, os.Environ()); execErr != nil {
		if backupPath != "" {
			if rbErr := os.Rename(backupPath, execPath); rbErr != nil {
				return false, fmt.Errorf("重启失败: %w；回滚原程序也失败: %v", execErr, rbErr)
			}
			return true, fmt.Errorf("重启失败: %w（已回滚，程序仍运行旧版本）", execErr)
		}
		return false, fmt.Errorf("重启失败: %w（程序仍运行旧版本）", execErr)
	}
	return false, nil
}
