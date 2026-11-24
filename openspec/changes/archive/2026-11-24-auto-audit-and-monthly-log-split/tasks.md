# Tasks

## 1. 操作审计按月拆分存储与检索

- [x] 1.1 在 `task_audit.go` 中重构日志路径解析逻辑，支持动态按月生成 `audit_YYYY-MM.jsonl`，并支持指定单文件或默认目录
- [x] 1.2 在 `task_audit.go` 中实现 `findAuditLogFiles` 与多文件跨月倒序聚合检索，并在 `task_audit_test.go` 中添加按月拆分与跨月读取单元测试

## 2. 任务向导默认开启日志审计

- [x] 2.1 在 `main.go` 中为任务向导添加 `wizardAuditCheck` 复选框（ID: `idWizardAuditCheck`），在新建与修改任务时均默认设置为选中状态
- [x] 2.2 在 `main.go` 的任务保存流程中集成日志审计状态判断，当勾选时自动检测并开启 `Microsoft-Windows-TaskScheduler/Operational` 事件日志通道

## 3. 验证与构建

- [x] 3.1 运行完整单元测试套件 `go test -count=1 ./...`，确保全部测试通过
- [x] 3.2 重新编译正式版与调试版二进制文件，验证构建无错误

## Workflow follow-up

- 归档本次变更 `openspec archive --yes "auto-audit-and-monthly-log-split"`
