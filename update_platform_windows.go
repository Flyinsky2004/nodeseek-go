//go:build windows

package main

import (
	"fmt"
	"os"
)

// installBinary cannot replace a running .exe on Windows. It saves the
// verified binary next to the executable and reports that a manual replace is
// required, rather than claiming the update was applied.
func installBinary(tempPath, execPath string) (installed bool, backupPath, newPath string, err error) {
	newPath = execPath + ".new"
	_ = os.Remove(newPath)
	if err := os.Rename(tempPath, newPath); err != nil {
		return false, "", "", fmt.Errorf("保存新版本失败: %w", err)
	}
	return false, "", newPath, nil
}

// restartProcess is not supported on Windows.
func restartProcess(execPath, backupPath string) (rolledBack bool, err error) {
	return false, fmt.Errorf("Windows 不支持自动替换正在运行的程序，请手动替换并重启")
}
