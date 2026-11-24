# Design

## Context

在先前的实现中，系统支持将“运行历史”和“操作审计”导出为 CSV 文件，但默认限制导出最新的 200 条记录。
当前代码中：
- `main.go` 中的 `exportLogAction()` 针对两个面板直接传入硬编码的 `200` 条数。
- `task_eventlog.go` 中的 `queryTaskExecutionHistory(taskName, limit)` 会在 `limit > 0` 时将 `wevtutil` 参数添加 `/c:<limit*3>`，并在返回结果时切片至 `limit` 条。
- `task_audit.go` 中的 `readRecentAuditRecords(logPath, taskName, limit)` 当 `limit > 0` 时截断至 `limit` 条，若 `limit <= 0` 则自然读取全部记录。

## Goals / Non-Goals

**Goals:**
- 在用户触发“导出日志”时，先弹出输入框交互对话框，展示数量输入框和醒目的规则说明。
- 支持输入 `0` 或 `-1` 代表“不限制数量（全部导出）”。
- 输入框默认预填 `200`，允许用户输入任意正整数。
- 在底层查询引擎中完美支持 `limit <= 0`（全量导出），去除 `wevtutil` 的 `/c:` 条数参数，避免截断，并提升执行超时时间保障全量查询完成。
- 提供完善的输入校验逻辑与纯函数单元测试。

**Non-Goals:**
- 不改变主界面当前选卡列表的预览数量（预览列表依然保持轻量化展示）。
- 不引入第三方 GUI 依赖库，保持与 Windows Server 2008 R2 到 Windows 11 全系统的无缝兼容。

## Decisions

### 1. 采用 PowerShell STA 弹窗交互展示说明与输入框
- **决策**：复用项目既有的 `runExternalPicker` 架构，通过包含 Windows Forms 的 PowerShell 轻量进程弹出输入窗口。
- **界面元素**：
  - Label：`请输入要导出的最大记录条数：\r\n\r\n提示：输入 0 或 -1 表示导出全部（不限制数量）。`
  - TextBox：默认填充 `200`。
  - Button：`确定`（`DialogResult.OK`，设为 `AcceptButton`）与 `取消`（`DialogResult.Cancel`，设为 `CancelButton`）。
- **替代方案考虑**：
  - Win32 原生 InputBox：Win32 标准 API 没有简单的通用多行说明输入框，需要自定义窗口类与控件重绘，在无 CGO 纯 Go 下代码繁重且易出现高 DPI 缩放问题。PowerShell STA 方案已在文件夹选择等场景验证，极具稳定性且代码干净。

### 2. 导出条数输入解析逻辑（`parseExportLimitInput`）
- **解析规则**：
  - 去除首尾空白字符。
  - 若为空（用户直接清空并回车确认），采用默认值 `200`。
  - 若为 `"0"` 或 `"-1"`，统一映射为 `-1`（内部表示无限制）。
  - 若为大于 0 的有效整数，返回该整数值。
  - 若为其他负数（如 `-2`）或非数字字符，返回格式解析错误。

### 3. 底层查询引擎对于“无限制”的处理
- **`task_eventlog.go`**：
  - 当 `limit > 0` 时，追加 `/c:<limit*3>` 并裁剪结果。
  - 当 `limit <= 0` 时，不追加 `/c:` 参数，并将超时时限由 8 秒放宽至 30 秒，防止大型历史日志被超时强杀。
  - 解析完成后直接返回全部配对的记录列表，不执行 `records = records[:limit]`。
- **`task_audit.go`**：
  - `readRecentAuditRecords` 已具备 `if limit > 0 && len(records) >= limit` 逻辑，当传入 `-1` 或 `0` 时即为全量读取，无需改动核心读取循环。

## Risks / Trade-offs

- **[Risk]** 当 Windows 事件日志极其庞大时，全量导出查询可能耗时数秒。
  - **Mitigation**: `exportLogAction` 本身已在独立 Goroutine 中异步运行，执行完毕后弹出成功/失败提示框，UI 线程不会假死或未响应；并且查询超时设置为 30 秒，兼顾了性能与鲁棒性。
