package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	daemonTaskName      = `\TaskSchedulerManager_LogDaemon`
	trayLogonTaskName   = `\TaskSchedulerManager_Tray`
	runRegistryKeyPath  = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	runRegistryValName  = `TaskSchedulerManager`
	monitorGlobalMutex  = `Global\TaskSchedulerManager_Monitor_Mutex`
	monitorLocalMutex   = `Local\TaskSchedulerManager_Monitor_Mutex`
	errorAlreadyExists  = syscall.Errno(183)
	errorAccessDenied   = syscall.Errno(5)
	winMainClassNameStr = "TaskSchedulerManagerWindow"
)

var (
	kernel32DLL      = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW = kernel32DLL.NewProc("CreateMutexW")
	procCloseHandle  = kernel32DLL.NewProc("CloseHandle")
)

// NamedMutex 封装 Windows 命名互斥锁，用于防止守护进程与桌面端重复轮询采集。
type NamedMutex struct {
	handle  syscall.Handle
	name    string
	isOwner bool
}

// acquireNamedMutex 尝试创建或打开指定名称的 Windows 互斥锁。
// 若已存在则返回 isOwner=false（不报错），调用方需在退出时调用 Release()。
func acquireNamedMutex(name string) (*NamedMutex, error) {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	r1, _, lastErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(namePtr)))
	if r1 == 0 {
		return nil, lastErr
	}
	h := syscall.Handle(r1)
	if lastErr == errorAlreadyExists {
		// 互斥锁已被其他进程持有，立即释放本进程句柄
		_, _, _ = procCloseHandle.Call(uintptr(h))
		return &NamedMutex{handle: 0, name: name, isOwner: false}, nil
	}
	return &NamedMutex{handle: h, name: name, isOwner: true}, nil
}

// acquireMonitorMutex 尝试获取全局采集互斥锁；若无权限访问 Global\ 命名空间则降级为 Local\。
func acquireMonitorMutex() (*NamedMutex, error) {
	m, err := acquireNamedMutex(monitorGlobalMutex)
	if err == nil {
		return m, nil
	}
	if errors.Is(err, errorAccessDenied) {
		return acquireNamedMutex(monitorLocalMutex)
	}
	return acquireNamedMutex(monitorLocalMutex)
}

func (m *NamedMutex) Release() {
	if m == nil || m.handle == 0 {
		return
	}
	_, _, _ = procCloseHandle.Call(uintptr(m.handle))
	m.handle = 0
	m.isOwner = false
}

// parseStartupFlags 解析程序命令行启动参数（--daemon / --minimized / --interval / --no-elevate）。
type startupOptions struct {
	Daemon    bool
	Minimized bool
	NoElevate bool
	Interval  time.Duration
}

func parseStartupFlags(args []string) startupOptions {
	opts := startupOptions{
		Interval: defaultMonitorInterval,
	}
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--daemon":
			opts.Daemon = true
		case arg == "--minimized":
			opts.Minimized = true
		case arg == "--no-elevate":
			opts.NoElevate = true
		case arg == "--interval" && i+1 < len(args):
			if sec, err := strconv.Atoi(strings.TrimSpace(args[i+1])); err == nil && sec >= 2 && sec <= 3600 {
				opts.Interval = time.Duration(sec) * time.Second
			}
			i++
		case strings.HasPrefix(arg, "--interval="):
			val := strings.TrimPrefix(arg, "--interval=")
			if sec, err := strconv.Atoi(strings.TrimSpace(val)); err == nil && sec >= 2 && sec <= 3600 {
				opts.Interval = time.Duration(sec) * time.Second
			}
		}
	}
	return opts
}

// buildDaemonAutoStartArgs 构造开机无人登录后台守护计划任务（SYSTEM / ONSTART）的 schtasks 参数。
func buildDaemonAutoStartArgs(exePath string) []string {
	tr := fmt.Sprintf(`"%s" --daemon`, strings.Trim(strings.TrimSpace(exePath), `"`))
	return []string{
		"/create",
		"/tn", daemonTaskName,
		"/tr", tr,
		"/sc", "ONSTART",
		"/ru", "SYSTEM",
		"/rl", "HIGHEST",
		"/f",
	}
}

// buildTrayLogonAutoStartArgs 构造用户登录后静默最小化到系统托盘（HIGHEST / ONLOGON，无 UAC 弹窗）的 schtasks 参数。
func buildTrayLogonAutoStartArgs(exePath string) []string {
	tr := fmt.Sprintf(`"%s" --minimized`, strings.Trim(strings.TrimSpace(exePath), `"`))
	return []string{
		"/create",
		"/tn", trayLogonTaskName,
		"/tr", tr,
		"/sc", "ONLOGON",
		"/rl", "HIGHEST",
		"/f",
	}
}

func runHiddenReg(args ...string) (string, error) {
	cmd := exec.Command("reg.exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return runCommandWithTimeout(cmd, metadataCommandTimeout)
}

func isRegistryAutoStartEnabled() bool {
	_, err := runHiddenReg("query", runRegistryKeyPath, "/v", runRegistryValName)
	return err == nil
}

// isAutoStartEnabled 检查本程序是否已开启开机自启（开机守护任务、登录托盘任务或注册表 Run 任一存在即视为已开启）。
func isAutoStartEnabled() bool {
	if taskExists(daemonTaskName) || taskExists(trayLogonTaskName) {
		return true
	}
	return isRegistryAutoStartEnabled()
}

// autoStartStatusDetail 返回当前开机自启配置模式的简短描述。
func autoStartStatusDetail() string {
	hasDaemon := taskExists(daemonTaskName)
	hasTray := taskExists(trayLogonTaskName)
	hasReg := isRegistryAutoStartEnabled()
	switch {
	case hasDaemon && hasTray:
		return "已开启（开机守护 + 托盘自启）"
	case hasDaemon:
		return "已开启（开机后台守护）"
	case hasTray || hasReg:
		return "已开启（用户登录托盘）"
	default:
		return "未开启"
	}
}

// enableAutoStart 注册开机自启：
// 1. 管理员权限下：注册 SYSTEM/ONSTART 后台守护计划任务（服务器重启无人登录即可记录日志）+ ONLOGON 托盘静默自启，并立即启动守护任务；
// 2. 非管理员权限下：降级写入 HKCU\...\Run 实现当前用户登录自启到托盘。
func enableAutoStart() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("无法获取当前程序路径：%w", err)
	}
	if isElevated() {
		daemonArgs := buildDaemonAutoStartArgs(exePath)
		if out, err := runSchtasks(daemonArgs...); err != nil {
			return "", fmt.Errorf("注册开机守护任务失败：%s", strings.TrimSpace(out))
		}
		// 同步注册登录托盘静默任务（失败不阻断核心开机守护）
		trayArgs := buildTrayLogonAutoStartArgs(exePath)
		trayMsg := ""
		if _, err := runSchtasks(trayArgs...); err != nil {
			debugLog("register tray logon task failed: %v", err)
			trayMsg = "（开机守护已就绪；登录托盘任务未注册）"
		} else {
			trayMsg = "（含无人登录开机守护 + 桌面登录托盘常驻）"
		}
		// 清理可能遗留的普通用户注册表项避免重复拉起
		_, _ = runHiddenReg("delete", runRegistryKeyPath, "/v", runRegistryValName, "/f")
		// 立即尝试触发一次守护任务，如果当前 GUI 已在运行内置采集器则由互斥锁无缝协调
		_, _ = runSchtasks("/run", "/tn", daemonTaskName)
		recordAudit("ENABLE_AUTOSTART", daemonTaskName, true, "开启开机自启"+trayMsg, "")
		return "已成功开启开机自启" + trayMsg + "。", nil
	}

	// 非管理员降级为当前用户登录自启
	val := fmt.Sprintf(`"%s" --minimized --no-elevate`, exePath)
	if out, err := runHiddenReg("add", runRegistryKeyPath, "/v", runRegistryValName, "/t", "REG_SZ", "/d", val, "/f"); err != nil {
		return "", fmt.Errorf("写入登录自启注册表失败：%s", strings.TrimSpace(out))
	}
	recordAudit("ENABLE_AUTOSTART", runRegistryValName, true, "开启当前用户登录自启（标准权限）", "")
	return "已开启当前用户登录自启（标准权限模式）。\r\n如需服务器无人登录开机后台记录，请以管理员身份运行后重新开启。", nil
}

// disableAutoStart 卸载开机自启守护任务、登录托盘任务及注册表自启项，并停止正在运行的守护任务。
func disableAutoStart() error {
	var errs []string
	if taskExists(daemonTaskName) {
		_, _ = runSchtasks("/end", "/tn", daemonTaskName)
		if out, err := runSchtasks("/delete", "/tn", daemonTaskName, "/f"); err != nil {
			errs = append(errs, "删除开机守护任务失败："+strings.TrimSpace(out))
		}
	}
	if taskExists(trayLogonTaskName) {
		if out, err := runSchtasks("/delete", "/tn", trayLogonTaskName, "/f"); err != nil {
			errs = append(errs, "删除登录托盘任务失败："+strings.TrimSpace(out))
		}
	}
	if isRegistryAutoStartEnabled() {
		if out, err := runHiddenReg("delete", runRegistryKeyPath, "/v", runRegistryValName, "/f"); err != nil {
			errs = append(errs, "删除注册表自启项失败："+strings.TrimSpace(out))
		}
	}
	if len(errs) > 0 {
		msg := strings.Join(errs, "；")
		recordAudit("DISABLE_AUTOSTART", daemonTaskName, false, "关闭开机自启失败", msg)
		return errors.New(msg)
	}
	recordAudit("DISABLE_AUTOSTART", daemonTaskName, true, "已关闭开机自启与后台守护任务", "")
	return nil
}

// runDaemonMode 启动无界面后台静默采集守护进程，持续按天记录本机全部定时任务执行日志。
func runDaemonMode(interval time.Duration) {
	debugLog("daemon mode starting (interval=%s)", interval)
	mutex, err := acquireMonitorMutex()
	if err != nil {
		debugLog("daemon failed to acquire mutex: %v", err)
		return
	}
	if !mutex.isOwner {
		debugLog("daemon exiting: another monitor instance already holds %s", mutex.name)
		return
	}
	defer mutex.Release()

	recordAudit("DAEMON_START", daemonTaskName, true, fmt.Sprintf("后台日志采集守护进程启动（周期 %s）", interval), "")
	mon := newTaskExecMonitor("", interval, queryTasks)
	mon.Start()

	// 阻塞常驻直到进程被系统停止
	select {}
}
