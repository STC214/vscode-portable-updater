package main

import (
	"fmt"
	"os"
	"runtime"
)

func main() {
	runtime.LockOSThread()

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
