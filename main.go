package main

import (
	"embed"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

// acquireSingleInstance 通过 Windows 命名互斥体确保只运行一个实例
func acquireSingleInstance() (syscall.Handle, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	createMutex := kernel32.NewProc("CreateMutexW")
	name, _ := syscall.UTF16PtrFromString("Global\\SY_KingAddin_SingleInstance")
	h, _, err := createMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return 0, fmt.Errorf("创建互斥体失败: %v", err)
	}
	// ERROR_ALREADY_EXISTS = 183
	if err.(syscall.Errno) == 183 {
		syscall.CloseHandle(syscall.Handle(h))
		return 0, fmt.Errorf("程序已在运行")
	}
	return syscall.Handle(h), nil
}

func main() {
	mutex, err := acquireSingleInstance()
	if err != nil {
		// 弹窗提示用户
		user32 := syscall.NewLazyDLL("user32.dll")
		msgBox := user32.NewProc("MessageBoxW")
		title, _ := syscall.UTF16PtrFromString("盛云王牌插件")
		text, _ := syscall.UTF16PtrFromString("程序已在运行中，请勿重复启动！")
		msgBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x30) // MB_ICONWARNING
		os.Exit(0)
	}
	defer syscall.CloseHandle(mutex)
	app := NewApp()

	err = wails.Run(&options.App{
		Title:             "盛云王牌插件",
		Width:             1000,
		Height:            640,
		MinWidth:          700,
		MinHeight:         400,
		DisableResize:     false,
		HideWindowOnClose: true, // 点 ✕ 只隐藏窗口缩小到托盘，不退出进程
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 13, G: 13, B: 13, A: 255},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
