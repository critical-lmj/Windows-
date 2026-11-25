package main

import (
	"syscall"
	"unsafe"

	"github.com/lxn/win"
)

const (
	nimAdd        = 0x00000000
	nimModify     = 0x00000001
	nimDelete     = 0x00000002
	nifMessage    = 0x00000001
	nifIcon       = 0x00000002
	nifTip        = 0x00000004
	nifInfo       = 0x00000010
	niifInfo      = 0x00000001
	mfString      = 0x00000000
	mfSeparator   = 0x00000800
	mfChecked     = 0x00000008
	mfUnchecked   = 0x00000000
	tpmRightBtn   = 0x0002
	tpmBottomAlgn = 0x0020
	trayIconUID   = 1
)

type winGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type notifyIconDataW struct {
	CbSize           uint32
	HWnd             win.HWND
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            win.HICON
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         winGUID
	HBalloonIcon     win.HICON
}

var (
	shell32TrayDLL       = syscall.NewLazyDLL("shell32.dll")
	procShellNotifyIconW = shell32TrayDLL.NewProc("Shell_NotifyIconW")

	user32TrayDLL       = syscall.NewLazyDLL("user32.dll")
	procCreatePopupMenu = user32TrayDLL.NewProc("CreatePopupMenu")
	procAppendMenuW     = user32TrayDLL.NewProc("AppendMenuW")
	procTrackPopupMenu  = user32TrayDLL.NewProc("TrackPopupMenu")
	procDestroyMenu     = user32TrayDLL.NewProc("DestroyMenu")
	procGetCursorPos    = user32TrayDLL.NewProc("GetCursorPos")
	procFindWindowW     = user32TrayDLL.NewProc("FindWindowW")

	trayIconAdded    bool
	trayBalloonShown bool
)

func copyUTF16ToFixed(dst []uint16, s string) {
	u := syscall.StringToUTF16(s)
	n := len(u)
	if n > len(dst) {
		n = len(dst)
	}
	copy(dst[:n], u[:n])
	if len(dst) > 0 {
		dst[len(dst)-1] = 0
	}
}

// initTrayIcon 向 Windows 右下角系统托盘注册常驻图标。
func initTrayIcon(hwnd win.HWND, tooltip string) {
	if hwnd == 0 {
		return
	}
	var nid notifyIconDataW
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = hwnd
	nid.UID = trayIconUID
	nid.UFlags = nifMessage | nifIcon | nifTip
	nid.UCallbackMessage = msgTrayCallback
	nid.HIcon = win.LoadIcon(0, win.MAKEINTRESOURCE(win.IDI_APPLICATION))
	copyUTF16ToFixed(nid.SzTip[:], tooltip)

	ret, _, _ := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	if ret != 0 {
		trayIconAdded = true
	}
}

// updateTrayTooltip 更新系统托盘鼠标悬停提示文案。
func updateTrayTooltip(hwnd win.HWND, tooltip string) {
	if !trayIconAdded || hwnd == 0 {
		return
	}
	var nid notifyIconDataW
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = hwnd
	nid.UID = trayIconUID
	nid.UFlags = nifTip
	copyUTF16ToFixed(nid.SzTip[:], tooltip)
	_, _, _ = procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

// showTrayBalloon 在首次隐藏至托盘时显示气泡提示。
func showTrayBalloon(hwnd win.HWND, title, body string) {
	if !trayIconAdded || hwnd == 0 || trayBalloonShown {
		return
	}
	trayBalloonShown = true
	var nid notifyIconDataW
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = hwnd
	nid.UID = trayIconUID
	nid.UFlags = nifInfo
	nid.DwInfoFlags = niifInfo
	copyUTF16ToFixed(nid.SzInfoTitle[:], title)
	copyUTF16ToFixed(nid.SzInfo[:], body)
	_, _, _ = procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

// removeTrayIcon 退出程序时从系统托盘卸载图标，避免残留死图标。
func removeTrayIcon(hwnd win.HWND) {
	if !trayIconAdded || hwnd == 0 {
		return
	}
	var nid notifyIconDataW
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = hwnd
	nid.UID = trayIconUID
	_, _, _ = procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	trayIconAdded = false
}

// showTrayContextMenu 在鼠标当前位置弹出系统托盘右键菜单。
func showTrayContextMenu(hwnd win.HWND) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	_, _, _ = procAppendMenuW.Call(menu, mfString, idTrayShow, uintptr(unsafe.Pointer(utf16("显示主窗口"))))
	_, _, _ = procAppendMenuW.Call(menu, mfString, idTrayOpenLogs, uintptr(unsafe.Pointer(utf16("打开日志目录"))))

	autoFlags := uintptr(mfString | mfUnchecked)
	if cachedAutoStartOn {
		autoFlags = mfString | mfChecked
	}
	_, _, _ = procAppendMenuW.Call(menu, autoFlags, idTrayAutoStart, uintptr(unsafe.Pointer(utf16("开机自启（后台守护）"))))
	_, _, _ = procAppendMenuW.Call(menu, mfSeparator, 0, 0)
	_, _, _ = procAppendMenuW.Call(menu, mfString, idTrayExit, uintptr(unsafe.Pointer(utf16("退出程序"))))

	var pt win.POINT
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	win.SetForegroundWindow(hwnd)
	_, _, _ = procTrackPopupMenu.Call(menu, tpmRightBtn|tpmBottomAlgn, uintptr(pt.X), uintptr(pt.Y), 0, uintptr(hwnd), 0)
	win.PostMessage(hwnd, win.WM_NULL, 0, 0)
}

// restoreMainWindowFromTray 从托盘恢复显示主窗口并置前。
func restoreMainWindowFromTray(hwnd win.HWND) {
	if hwnd == 0 {
		return
	}
	win.ShowWindow(hwnd, win.SW_RESTORE)
	win.ShowWindow(hwnd, win.SW_SHOW)
	win.SetForegroundWindow(hwnd)
}

// tryActivateExistingMainWindow 查找已运行的同类名主窗口；若找到则向其发送恢复显示消息并返回 true。
func tryActivateExistingMainWindow() bool {
	existing, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(utf16(winMainClassNameStr))), 0)
	if existing == 0 {
		return false
	}
	win.PostMessage(win.HWND(existing), msgRestoreWindow, 0, 0)
	return true
}
