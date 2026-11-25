# Proposal

## Why

此前项目的“运行历史”依赖查询 Windows 自带的事件日志通道（`Microsoft-Windows-TaskScheduler/Operational`），存在系统默认关闭、事件日志易被系统轮转覆盖、以及不同 Windows 版本事件字段差异大等问题。用户希望彻底脱离系统自带事件日志，由本软件自身独立留存**本机全部定时任务**的运行日志（按天分文件存储），同时具备**开机自启（含服务器无人登录后台守护）**与**系统托盘后台常驻**能力，保证长期稳定、全量、可追溯地记录定时任务执行历史。

## What Changes

- **独立后台定时轮询采集引擎（零侵入、全任务覆盖、按天存储）**：
  - 后台定时扫描本机所有 Windows 定时任务（不修改原有任务命令，不读取系统自带事件日志），检测任务运行状态变化（`Running` 开始、运行结束）及 `LastRunTime` / `LastResult` 变更。
  - 将捕获到的全部定时任务（含自定义与系统级任务）执行留痕以 JSON Lines 格式按天拆分写入 `logs/exec_YYYY-MM-DD.jsonl`。
  - 通过 `logs/monitor_state.json` 持久化状态基线，防止重启重复记录，并基于 `InstanceID` 合并运行中与最终完成状态。
- **彻底脱离系统自带事件日志（`wevtutil`）**：
  - 主界面“运行历史”页签与“导出日志（运行历史 CSV）”完全改为从本地按天文件 `logs/exec_YYYY-MM-DD.jsonl` 跨天倒序聚合读取，不再调用 `wevtutil.exe` 读取系统事件日志。
  - 未选中任务时在“运行历史”页签直接展示全局最近执行留痕；选中单个任务时展示该任务的历史执行记录。
- **后台静默守护进程（`--daemon`）与单实例互斥**：
  - 新增 `--daemon` 无界面守护模式（可配合 `--interval` 配置轮询间隔，默认 10 秒）。
  - 使用 Windows 全局命名互斥锁（`Global\TaskSchedulerManager_Monitor_Mutex`）确保守护进程与桌面 GUI 之间不会重复轮询和重复写入日志。
- **开机自启（服务器无人登录 + 桌面登录托盘）与系统托盘常驻**：
  - 实现纯 Win32 系统托盘图标（`Shell_NotifyIconW`），点击关闭按钮自动最小化至托盘后台常驻，右键托盘菜单提供“显示主窗口 / 开机自启开关 / 彻底退出”；重复双击程序时自动唤醒已隐藏在托盘的实例。
  - 新增一键“开机自启”配置能力：以 `SYSTEM / ONSTART` 注册后台守护计划任务（`\TaskSchedulerManager_LogDaemon`，服务器重启后无人登录也能自动采集日志），同时写入当前用户登录自启（`--minimized` 启动至托盘）。

## Capabilities

### New Capabilities

### Modified Capabilities
- `task-log-stream`: 将任务运行历史由读取 Windows 自带事件日志重构为独立后台轮询采集与按天落盘（`logs/exec_YYYY-MM-DD.jsonl`），并增加后台守护进程、系统托盘常驻与开机自启能力。

## Impact

- 新增模块：
  - `task_exec_monitor.go` / `task_exec_monitor_test.go`：状态快照对比、按天日志文件读写、跨天聚合去重读取与守护循环。
  - `autostart.go` / `autostart_test.go`：全局采集互斥锁、开机自启（`ONSTART` 守护任务 + 登录自启注册表）查询与开关。
  - `tray.go`：纯 Win32 系统托盘图标与右键弹出菜单。
- 修改模块：
  - `main.go` / `main_test.go`：接入 `--daemon` / `--minimized` 启动参数、单实例窗口唤醒、系统托盘常驻生命周期、主界面“运行历史”与“导出日志”切换至独立按天日志、“开机自启”按钮与状态栏展示。
- 兼容性：零 CGO、纯 Go 1.20，完整兼容 Windows Server 2008 R2 与 Windows 11。
