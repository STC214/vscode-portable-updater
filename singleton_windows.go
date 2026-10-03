package main

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"unsafe"
)

// One updater per machine, including copies in other directories/sessions.
const singleInstanceName = `Global\AutoUpdataVSCode.Singleton.v1`

var (
	errInstanceRunning = errors.New("程序已经在运行")
	procCreateMutexW   = kernel32.NewProc("CreateMutexW")
)

type instanceLock struct {
	handle syscall.Handle
	once   sync.Once
	err    error
}

func acquireSingleInstance(name string) (*instanceLock, error) {
	if name == "" {
		return nil, errors.New("单实例互斥体名称为空")
	}
	ptr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	// No thread ownership is required: keep the non-inheritable handle open for
	// the entire app lifetime. Windows destroys the object after the last close.
	handle, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(ptr)))
	if handle == 0 {
		return nil, fmt.Errorf("创建单实例互斥体失败: %w", callErr)
	}
	if errors.Is(callErr, syscall.ERROR_ALREADY_EXISTS) {
		if err := syscall.CloseHandle(syscall.Handle(handle)); err != nil {
			return nil, err
		}
		return nil, errInstanceRunning
	}
	return &instanceLock{handle: syscall.Handle(handle)}, nil
}

func (lock *instanceLock) Close() error {
	lock.once.Do(func() { lock.err = syscall.CloseHandle(lock.handle) })
	return lock.err
}
