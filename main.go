package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
)

func main() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	instance, err := acquireSingleInstance(singleInstanceName)
	if errors.Is(err, errInstanceRunning) {
		messageBox(0, "程序已经在运行，请使用已打开的窗口。", "AutoUpdate VSCode", MB_OK)
		return
	}
	if err != nil {
		showFatalError(fmt.Sprintf("单实例检查失败: %v", err))
		os.Exit(1)
	}
	defer instance.Close()

	app, err := NewApp()
	if err != nil {
		showFatalError(fmt.Sprintf("初始化失败: %v", err))
		os.Exit(1)
	}

	if err := app.Run(); err != nil {
		showFatalError(fmt.Sprintf("运行失败: %v", err))
		os.Exit(1)
	}
}
