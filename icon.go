package main

import (
	"syscall"
	"time"
)

func setConsoleIcon() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	user32 := syscall.NewLazyDLL("user32.dll")
	getModuleHandle := kernel32.NewProc("GetModuleHandleW")
	getConsoleWindow := kernel32.NewProc("GetConsoleWindow")
	loadIcon := user32.NewProc("LoadIconW")
	sendMessage := user32.NewProc("SendMessageW")
	setClassLongPtr := user32.NewProc("SetClassLongPtrW")
	setWindowPos := user32.NewProc("SetWindowPos")

	hInst, _, _ := getModuleHandle.Call(0)
	hIcon, _, _ := loadIcon.Call(hInst, 1)
	if hIcon == 0 {
		return
	}
	apply := func() {
		hwnd, _, _ := getConsoleWindow.Call()
		if hwnd == 0 {
			return
		}
		sendMessage.Call(hwnd, 0x0080, 1, hIcon)
		sendMessage.Call(hwnd, 0x0080, 0, hIcon)
		setClassLongPtr.Call(hwnd, ^uintptr(13), hIcon)
		setClassLongPtr.Call(hwnd, ^uintptr(33), hIcon)
		setWindowPos.Call(hwnd, 0, 0, 0, 0, 0, 0x0001|0x0002|0x0004|0x0020)
	}
	apply()
	go func() {
		for i := 0; i < 8; i++ {
			time.Sleep(350 * time.Millisecond)
			apply()
		}
	}()
}
