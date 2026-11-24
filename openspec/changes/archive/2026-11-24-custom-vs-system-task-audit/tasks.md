# Tasks

## 1. 任务分类与审计策略持久化

- [x] 1.1 在 `task_audit.go` 中实现 `isSystemTask` 任务分类函数（识别 `\Microsoft\` 路径与 `$(@` 资源作者）及审计状态文案 `taskAuditStatusText`
- [x] 1.2 在 `task_audit.go` 中实现 `AuditPolicyStore`（读写 `logs/audit_policy.json`），支持默认策略判定、手动开启/关闭覆盖、任务改名迁移与删除清理
- [x] 1.3 在 `task_audit_test.go` 中编写任务分类、默认策略及持久化覆盖单元测试

## 2. 界面展示、手动开关与通道联动

- [x] 2.1 在 `main.go` 主列表增加“日志审计”列，并在 `statusCombo` 筛选框增加“仅自定义任务”、“仅系统任务”、“仅已开审计”筛选条件
- [x] 2.2 在 `main.go` 工具栏新增 `toggleAuditButton`（“开启审计”/“关闭审计”），实现 `toggleTaskAuditAction` 切换、持久化与审计打点
- [x] 2.3 在 `main.go` 的 `msgTasksLoaded` 中加入自定义/已开启审计任务存在时的后台自动启用事件通道逻辑，并在运行历史面板、详情面板与导出日志流程中接入审计策略校验
- [x] 2.4 在 `main.go` 的任务保存、改名与删除流程及向导回显中同步维护任务审计策略
- [x] 2.5 在 `main_test.go` 中补充筛选逻辑与向导审计默认策略测试

## 3. 验证与构建

- [x] 3.1 运行完整单元测试套件 `go test -v -count=1 ./...`，确保全部测试通过
- [x] 3.2 编译正式版 `TaskSchedulerManager.exe` 与调试版 `TaskSchedulerManager-debug.exe`，验证构建无错误

## Workflow follow-up

- 归档本次变更 `openspec archive -y "custom-vs-system-task-audit"`
