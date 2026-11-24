# Proposal

## Why

现有 Windows 定时任务管理器仅展示单次快照属性（上次运行时间、上次运行结果），无法回溯任务在无人值守时的历史运行频次、执行耗时、退出代码，也无法追踪任务的创建时间以及运维人员在工具内的操作痕迹。

为满足运维巡检与故障排查需求，亟需为任务计划管理器引入两类关键日志流：基于系统底层事件日志的**任务执行历史流**，以及基于任务元数据与本地操作记录的**审计与创建时间流**。

## What Changes

- **新增任务创建时间展示**：从任务原生 XML（`RegistrationInfo.Date`）提取创建时间并在主列表与详情中展示。
- **新增执行历史流采集**：通过 `wevtutil.exe` 采集 Windows 原生 `Microsoft-Windows-TaskScheduler/Operational` 事件日志（Event ID 100/102/200/201），解析单次执行的起止时间、耗时和退出码。
- **新增历史记录通道状态自检与开启**：检测系统调度历史通道是否处于启用状态，支持一键提权启用。
- **新增管理操作审计流**：异步将新建、修改、启停、删除等运维操作记录至本地结构化日志文件 `logs/audit.jsonl`。
- **增强界面交互**：底栏详情区升级为【基础详情 / 运行历史 / 操作审计】多视图切换，支持点击联动与异步加载。

## Capabilities

### New Capabilities
- `task-log-stream`: 覆盖任务创建时间解析展示、系统执行历史事件日志查询与通道管理、以及本地运维管理操作审计日志流。

### Modified Capabilities
<!-- 无现有 capability 需求发生变更 -->

## Impact

- **代码影响**：新增 `task_eventlog.go`、`task_eventlog_test.go`、`task_audit.go`、`task_audit_test.go`；调整 `main.go` 中的数据模型（`Task`、`schedulerTaskXML`）及 Win32 控件布局。
- **系统依赖**：仅依赖 Windows 系统自带的 `wevtutil.exe` 与任务计划服务，无额外第三方外部依赖，保持对 Windows Server 2008 R2 的完全兼容。
- **存储影响**：在程序所在目录下生成 `logs/audit.jsonl` 文件。
