# Tasks

## 1. 核心 CSV 导出与单测

- [x] 1.1 编写 `TestExportTaskExecutionHistoryToCSV` 与 `TestExportAuditRecordsToCSV` 单测，覆盖 CSV 表头、数据格式与 UTF-8 BOM 编码
- [x] 1.2 在 `task_eventlog.go` 与 `task_audit.go` 中实现 CSV 导出功能函数

## 2. 界面按钮与交互接入

- [x] 2.1 在主窗口操作栏增加【导出日志】按钮，实现上下文识别与保存文件对话框调用
- [x] 2.2 运行全量测试并构建正式版与调试版可执行文件，验证功能正常
