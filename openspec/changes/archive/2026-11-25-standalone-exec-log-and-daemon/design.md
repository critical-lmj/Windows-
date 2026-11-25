# Design

## Context

此前版本使用 `wevtutil.exe` 读取 Windows `Microsoft-Windows-TaskScheduler/Operational` 事件日志来呈现任务运行历史。新需求要求：
1. 彻底不读取系统自带日志，由本程序独立记录本机**全部定时任务**的执行留痕；
2. 独立运行日志**按天拆分存储**（`logs/exec_YYYY-MM-DD.jsonl`）；
3. 支持**开机自启**与**后台常驻**（包含服务器无人登录场景下的静默守护进程 + 桌面端托盘常驻）。

## Goals / Non-Goals

**Goals:**
- 零侵入后台轮询本机全部定时任务，捕获所有任务的执行开始、结束、退出码与耗时并按天落盘。
- 彻底移除 UI 运行历史与导出链路对 `wevtutil.exe` 的依赖。
- 提供 `--daemon` 静默守护进程模式和 `--minimized` 托盘静默启动模式。
- 通过 Windows 全局命名互斥锁防止守护进程与桌面端同时轮询造成重复写日志。
- 提供纯 Win32 系统托盘常驻与一键“开机自启”配置（兼容服务器无人登录重启场景）。

**Non-Goals:**
- 不改写原有定时任务的执行命令，因此不采集目标脚本内部打印的 stdout/stderr 文本输出。
- 不依赖第三方托盘库或 CGO，全部使用标准库与 Win32 DLL 系统调用保证 Server 2008 R2 兼容。

## Decisions

### 1. 独立执行日志格式与按天拆分（`task_exec_monitor.go`）
- 文件路径：`logs/exec_YYYY-MM-DD.jsonl`（例如 `logs/exec_2026-11-25.jsonl`），放在程序所在目录 `logs/` 下。
- 状态基线持久化：`logs/monitor_state.json`，记录每个任务（按规范化小写路径索引）的 `Status`、`LastRun`、`LastResult`、`RunningAt`、`InstanceID`。
- 轮询 Diff 状态机（`diffTaskSnapshots`）：
  - **首次建立基线**（`monitor_state.json` 为空）：记录当前所有任务快照，不将陈旧的 `LastRunTime` 误判为新执行；若恰好正在运行（`Running`），记录运行中事件。
  - **进入运行中**：生成 `InstanceID`，记录 `RunningAt = now`，写入一条 `Status: "运行中"` 日志。
  - **从运行中结束**：计算 `now - RunningAt` 耗时，根据 `LastResult` 判定“成功”/“失败 (code)”/“完成”，以同一 `InstanceID` 写入最终完成记录。
  - **轮询间隔内完成**（未抓到 `Running`，但 `LastRunTime` 变化且非空/非 `N/A`）：生成 `InstanceID`，写入包含 `StartTime = LastRunTime`、`EndTime = now`、`ExitCode = LastResult` 的记录。
- 读取端去重（`readRecentExecRecords`）：
  - 扫描 `logs/exec_*.jsonl` 按文件名日期降序读取；单文件内倒序读取。
  - 维护 `seenInstanceIDs` 集合：由于倒序读取时“最终完成记录”总在早先的“运行中记录”之后写入（即倒序时先被读到），跳过同 `InstanceID` 的旧状态记录，使每次执行只呈现一条最新状态记录。

### 2. 双模式运行与全局互斥锁防重（`autostart.go`）
- **采集互斥锁**：调用 `kernel32.dll` `CreateMutexW` 创建 `Global\TaskSchedulerManager_Monitor_Mutex`（无管理员权限创建 `Global\` 失败时降级为 `Local\TaskSchedulerManager_Monitor_Mutex`）。
  - 抢占成功的进程启动后台轮询循环（默认每 10 秒一次，跳过上一轮尚未结束的重叠周期）。
  - 未抢占到锁的 GUI 进程识别到后台守护进程正在运行，直接展示“后台采集: 守护进程运行中”，避免双倍 CPU 与重复落盘。
- **GUI 单实例唤醒**：使用窗口类名查找已有主窗口，若用户再次双击打开程序，向已隐藏在托盘的窗口发送 `msgRestoreWindow` 将其置前恢复。

### 3. 开机自启架构（`autostart.go`）
- **后台静默守护任务（服务器无人登录核心保障）**：
  - 使用 `schtasks.exe /create /tn "\TaskSchedulerManager_LogDaemon" /tr "\"<exe>\" --daemon" /sc ONSTART /ru SYSTEM /rl HIGHEST /f` 注册开机即运行的系统级守护任务，并在开启时立即执行一次 `/run` 使其马上开始采集。
- **桌面登录托盘自启**：
  - 在 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` 写入 `TaskSchedulerManager = "\"<exe>\" --minimized"`，用户登录桌面后自动静默驻留右下角托盘。
- 关闭开机自启时，同时停止并删除 `\TaskSchedulerManager_LogDaemon` 任务及移除注册表 Run 键。

### 4. 纯 Win32 系统托盘常驻（`tray.go`）
- 通过 `shell32.dll` `Shell_NotifyIconW` 注册托盘图标，绑定自定义消息 `msgTrayCallback`。
- 点击主窗口右上角 `X`（`WM_CLOSE`）时默认隐藏窗口到托盘（`SW_HIDE`）；托盘右键弹出菜单包含：显示主窗口、开机自启（带勾选状态）、退出程序。点击“退出程序”才真正删除托盘图标并销毁窗口退出。

## Risks / Trade-offs

- **[Risk]** 每 10 秒调用一次 `schtasks.exe /query /fo LIST /v` 在任务极多的旧服务器上可能存在轻微 I/O 波动。
  - **Mitigation**: 轮询严格串行（上一轮完成后才等待下一轮间隔），且默认 10 秒间隔；同时提供 `--interval` 参数按需调优。
- **[Risk]** 轮询间隔内（<10s）同一任务被极端高频触发多次时，仅能捕获最新一次 `LastRunTime`。
  - **Mitigation**: 这是零侵入轮询方案的固有边界（已在决策阶段与用户确认）；对常规分钟级/小时级/天级定时任务可完整捕获。
- **[Risk]** 自启守护任务 `\TaskSchedulerManager_LogDaemon` 本身也是计划任务，是否会产生多余日志？
  - **Mitigation**: 守护任务仅在开机启动一次并常驻运行，几乎不会触发状态变化；且 `diffTaskSnapshots` 可选择性跳过自身守护任务或仅记录启动，无刷屏风险。
