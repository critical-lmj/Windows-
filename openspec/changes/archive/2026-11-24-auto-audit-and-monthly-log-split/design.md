# Design

## Context

在目前的系统中：
1. 定时任务的执行历史依赖 Windows 系统事件日志通道 `Microsoft-Windows-TaskScheduler/Operational`。由于 Windows 默认关闭该通道，用户过去必须在主界面手动点击“启用历史”，否则新建或修改的任务均无法在“运行历史”中捕获运行记录。
2. 管理工具自身的操作审计日志目前固定写入 `logs/audit.jsonl` 单一文件中，长期运行后该文件会持续增大，缺少按自然月份拆分归档的机制。

## Goals / Non-Goals

**Goals:**
- 在新建任务与修改任务弹窗中增加“开启日志审计（记录任务执行历史与运行状态）”复选框，默认处于勾选状态。
- 在保存新建或修改的任务时，只要勾选了日志审计，自动检测并确保系统事件日志通道 `Microsoft-Windows-TaskScheduler/Operational` 处于开启状态。
- 本地管理操作审计日志自动按月份动态分割命名存储（`logs/audit_YYYY-MM.jsonl`）。
- 检索与导出时支持透明跨月倒序聚合读取，并完全向下兼容已有的旧版 `audit.jsonl`。

**Non-Goals:**
- 不修改 Windows 事件查看器中事件日志的系统级轮转策略（Windows 自身的 Operational 通道由系统按大小轮转）。
- 不破坏现有审计日志的 JSON Lines 字段格式和 CSV 导出格式。

## Decisions

### 1. 任务向导中日志审计复选框交互与通道自愈
- **界面控件**：
  - 在 `createWizardControls` 中创建 `wizardAuditCheck`（控件 ID `idWizardAuditCheck = 2026`），标签为“开启日志审计（记录任务执行历史与运行状态）”。
  - 默认赋值：在新建任务创建控件时，发送 `win.BM_SETCHECK` 设为 `win.BST_CHECKED`；在修改已有任务 `populateWizard` 时，同样默认设为 `win.BST_CHECKED`。
- **自愈逻辑触发点**：
  - 在 `safeCreateBasicTask` 中获取复选框状态，传给 `saveTaskConfig`。
  - 在 `saveTaskConfig` 执行中，若开启了日志审计，查询 `isTaskHistoryChannelEnabled()`；若为 `false`，则在后台自动执行 `enableTaskHistoryChannel()`，并记录审计事件。

### 2. 本地审计日志按月份拆分命名与写入
- **命名规范**：`logs/audit_YYYY-MM.jsonl`（例如 `audit_2026-11.jsonl`）。
- **动态写入**：
  - `AuditLogger` 不再将单一文件路径写死，而是维护 `logDir`。
  - 写入工作协程 `writeToFile` 在每次写入时，根据当前记录的时间戳动态调用 `monthlyAuditLogPath(al.logDir, time.Now())` 获取目标文件，确保在跨月瞬间自动无缝写入新文件，无需重启程序。
  - 为兼容单元测试，若初始化显式传入了具体的 `.jsonl` 文件路径，则优先写入指定文件。

### 3. 多月份审计文件倒序聚合检索
- **文件扫描与排序 (`findAuditLogFiles`)**：
  - 读取指定目录（默认 `logs/`），筛选出符合 `audit_*.jsonl`、`audit-*.jsonl` 以及遗留的 `audit.jsonl`。
  - 月份文件按降序排序（如 `2026-12` -> `2026-11` -> `2026-10`），旧版单一 `audit.jsonl` 置于最后，确保优先检索最新时间段。
- **多文件倒序迭代**：
  - `readRecentAuditRecords` 遍历文件列表，每个文件内逆序读取行。
  - 逐行反序列化并进行任务名过滤。
  - 当满足 `limit > 0 && len(records) >= limit` 时及时终止；当 `limit <= 0`（全量导出）时完整合并所有月份日志后返回。

## Risks / Trade-offs

- **[Risk]** 用户在向导保存任务时，如果自动开启系统事件通道失败（如缺少管理员权限）是否会影响任务保存？
  - **Mitigation**: 尝试开启通道作为非阻塞增强，即便开启命令失败，也会捕获错误并写入审计记录，不会阻断定时任务本身的保存。
- **[Risk]** 历史 `audit.jsonl` 与新月度文件 `audit_YYYY-MM.jsonl` 并存时的重复或顺序混乱。
  - **Mitigation**: 新写入操作仅写入当月文件；历史 `audit.jsonl` 作为较早的历史记录合并在最后，保证时间顺序自然衔接。
