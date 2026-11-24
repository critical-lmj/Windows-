# Tasks

## 1. 查询引擎无限制适配

- [x] 1.1 在 `task_eventlog.go` 的 `queryTaskExecutionHistory` 中支持 `limit <= 0` 全量查询（去除 `/c:` 参数、提高查询超时至 30s、不对返回记录切片），并验证现有测试正常通过

## 2. 输入弹窗与参数解析实现

- [x] 2.1 在 `main.go` 中实现 `parseExportLimitInput(input string) (int, error)`，处理 `0`/`-1` 映射为 `-1`、空输入默认 `200` 以及非法字符校验
- [x] 2.2 在 `main.go` 中实现基于 PowerShell WinForms 的条数输入弹窗 `promptExportLimitDialog(title, defaultVal string) (int, bool)`，并在窗口内明确渲染“提示：输入 0 或 -1 表示导出全部（不限制数量）”
- [x] 2.3 在 `main.go` 的 `exportLogAction` 中集成 `promptExportLimitDialog` 调用，将用户设定的条数传递给日志和审计查询接口

## 3. 单元测试与系统验证

- [x] 3.1 在 `main_test.go` 中添加 `TestParseExportLimitInput` 单元测试，覆盖 0、-1、正整数、空输入及非法输入边界
- [x] 3.2 运行完整自动化测试套件 `go test -count=1 ./...`，确保全部测试通过
- [x] 3.3 重新编译正式版与调试版二进制文件，验证构建无错误

## Workflow follow-up

- 归档本次变更 `openspec archive --yes "custom-export-limit"`
