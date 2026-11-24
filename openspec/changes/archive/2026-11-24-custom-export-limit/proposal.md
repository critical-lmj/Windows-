# Proposal

## Why

用户在导出任务运行历史或管理操作审计日志时，默认固定导出 200 条无法满足全量追溯或更长周期归档的需求。为了给予用户更灵活的控制权，系统需要支持用户自定义填写导出的最大记录条数；当用户希望导出全部历史数据时，支持输入 `0` 或 `-1` 代表不设数量限制，并在输入弹窗上直观提示该规则，避免用户困惑或重复尝试。

## What Changes

- **自定义导出数量弹窗交互**：用户在点击“导出日志”时，先弹出自定义条数输入框（默认值为 200），用户可输入正整数条数，或输入 `0` / `-1` 导出全部记录。
- **弹窗界面规则提示**：在输入框上方清晰展示提示说明：“提示：输入 0 或 -1 表示导出全部（不限制数量）”。
- **无限制日志拉取底层适配**：
  - `task_eventlog.go`：当 `limit <= 0` 时，移除 `wevtutil qe` 的 `/c:...` 截断参数，调整执行超时时间（避免长日志被 8s 超时截断），并不对解析结果切片。
  - `task_audit.go`：当 `limit <= 0` 时读取全部匹配审计记录。
- **健壮输入解析**：解析空输入、非法字符串、负数等边界情况并给予友好提示与处理。

## Capabilities

### New Capabilities

### Modified Capabilities
- `task-log-stream`: 扩展日志导出能力，增加自定义导出数量及 `0`/`-1` 全量无限制导出机制，并在交互弹窗中明确展示无限制导出规则。

## Impact

- 代码受影响模块：
  - `main.go`：增加条数输入弹窗逻辑 `promptExportLimitDialog`、解析函数 `parseExportLimitInput`，并在 `exportLogAction` 中调用。
  - `task_eventlog.go`：调整 `queryTaskExecutionHistory` 对 `limit <= 0`（全量导出）的处理分支。
  - `main_test.go`：增加输入解析边界用例单元测试。
- 外部依赖与环境：无新增外部依赖，完全保持 Windows 原生 Win32/PowerShell STA 弹窗兼容性与纯 Go 编译环境。
