# Proposal

## Why

1. 在管理 Windows 定时任务时，Windows 默认禁用了 `Microsoft-Windows-TaskScheduler/Operational` 执行历史日志通道，导致新建或修改的任务无法记录执行耗时、退出码与执行状态。用户希望在新建和修改任务时默认开启日志审计，免去手动维护的麻烦。
2. 管理运维操作审计日志如果持续写入单个文件，文件体积会随着时间线性增长，不利于按月份归档、备份和安全清理。需要将日志文件拆分为按月独立存储（例如 `audit_2026-11.jsonl`），同时保持全局读取与导出的完整性。

## What Changes

- **新建与修改任务默认开启日志审计**：
  - 在新建/修改任务配置弹窗（Wizard）中增加“开启日志审计（记录任务执行历史与运行状态）”选项，默认勾选。
  - 在保存任务（创建或修改）时，若开启日志审计，自动检测系统 `Microsoft-Windows-TaskScheduler/Operational` 通道状态；若未启用则自动调用命令开启，确保任务后续执行具备可追溯的审计记录。
- **管理操作审计日志按月拆分存储**：
  - 本地操作审计日志写入目标由固定单一文件更改为按月份动态滚动：`logs/audit_YYYY-MM.jsonl`。
  - 倒序读取（界面列表与 CSV 导出）支持自动扫描 `logs/` 下所有按月拆分文件，按时间最新倒序合并读取，并无缝向前兼容旧版 `audit.jsonl`。

## Capabilities

### New Capabilities

### Modified Capabilities
- `task-log-stream`: 增加新建与修改任务默认自动启用底层日志审计通道的要求，并将管理操作审计日志升级为按月滚动拆分存储与检索机制。

## Impact

- 代码受影响模块：
  - `main.go`：任务向导窗口界面控件与保存逻辑适配；
  - `task_audit.go`：按月拆分路径计算、动态文件写入与多文件聚合读取；
  - `task_audit_test.go` & `main_test.go`：新增多月份读写与向导状态测试。
- 外部依赖与系统兼容性：
  - 完全保持无 CGO、纯 Go 编译，兼容 Windows Server 2008 R2 到 Windows 11。
