# Tasks

## 1. 独立按天执行日志与轮询状态机

- [x] 1.1 新增 `task_exec_monitor.go`，实现按天日志文件命名（`logs/exec_YYYY-MM-DD.jsonl`）、跨天倒序聚合与 `InstanceID` 去重读取（`readRecentExecRecords`）
- [x] 1.2 在 `task_exec_monitor.go` 中实现基线持久化（`logs/monitor_state.json`）、状态对比算法（`diffTaskSnapshots`）及后台串行轮询器（`TaskExecMonitor`），记录本机全部定时任务执行留痕
- [x] 1.3 新增 `task_exec_monitor_test.go`，覆盖按天分文件写入、跨天倒序去重读取、首次冷启动基线、运行中->完成耗时计算及短周期直接完成捕获单元测试

## 2. 全局互斥锁、守护模式与开机自启

- [x] 2.1 新增 `autostart.go`，实现 Windows 命名互斥锁封装（防守护进程与桌面端重复采集）、`ONSTART` 系统级守护任务注册/卸载与 `HKCU\...\Run` 托盘登录自启管理
- [x] 2.2 新增 `autostart_test.go`，覆盖互斥锁互斥行为、守护命令行参数构造及自启命令解析单元测试

## 3. 系统托盘常驻与主界面集成

- [x] 3.1 新增 `tray.go`，基于 Win32 `Shell_NotifyIconW` 实现系统托盘图标、左键/双击恢复窗口与右键菜单（显示主窗口、开机自启开关、退出程序）
- [x] 3.2 修改 `main.go`，支持 `--daemon` / `--interval` / `--minimized` 启动参数及 GUI 单实例唤醒；点击关闭按钮隐藏至托盘常驻
- [x] 3.3 修改 `main.go`，将“运行历史”页签与“导出日志（运行历史 CSV）”完全切换为读取本地按天文件 `logs/exec_YYYY-MM-DD.jsonl`（不再调用 `wevtutil`），并将原“启用历史”按钮升级为“开机自启”开关及展示后台采集状态

## 4. 验证、构建与文档更新

- [x] 4.1 运行完整自动化测试套件 `go test -v -count=1 ./...`，确保全部测试通过
- [x] 4.2 重新编译正式版 `TaskSchedulerManager.exe` 与调试版 `TaskSchedulerManager-debug.exe` 并验证 `--daemon` / 测试通过
- [x] 4.3 同步更新 `README.md`、`go-task-scheduler-manager-项目介绍.md` 与 `docs/Git上传清单与发布指南.md`

## Workflow follow-up

- 归档本次变更 `openspec archive -y "standalone-exec-log-and-daemon"`
