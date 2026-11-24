# Git 上传清单与发布指南

本文档说明“Windows 定时任务管理器”哪些文件应提交到源码仓库，哪些文件只应作为 Release 附件发布，以及常用 Git 操作步骤。

## 1. 源码仓库必须上传的文件

### Go 源码与测试

```text
main.go                 主窗口、任务查询和任务管理逻辑
task_archive.go         任务 XML 导入、导出和 ZIP 备份逻辑
task_audit.go           本地管理操作按月审计流与单任务审计策略管理
task_eventlog.go        基于 Windows 事件日志的任务执行历史查询与配对
main_test.go            主程序的自动化测试
task_archive_test.go    导入、导出和归档逻辑的自动化测试
task_audit_test.go      按月审计日志与差异化策略的自动化测试
task_eventlog_test.go   事件日志解析、配对与导出的自动化测试
go.mod                  Go 版本与依赖声明
go.sum                  依赖校验记录
```

`*_test.go` 文件不参与最终 exe 的运行，但应上传。开发者可通过 `go test ./...` 验证修改没有破坏已有功能。

### 构建、配置与文档

```text
build.bat
prepare-portable-go.bat
.gitignore
.gitattributes
README.md
权限和筛选说明.md
go-task-scheduler-manager-项目介绍.md
docs/
```

其中 `docs/` 包含导入导出功能的设计、实施计划和本说明文档，应与源码一起保存，便于后续维护。

## 2. 不应提交到 Git 的文件

以下文件由 `.gitignore` 排除：

```text
TaskSchedulerManager.exe
TaskSchedulerManager-debug.exe
TaskSchedulerManager-*.exe
debug.log
*.log
logs/
.agents/
.runtime/
*.tmp
.vscode/
.idea/
```

说明：

- `.runtime/` 是便携 Go 工具链、依赖缓存和编译缓存，体积大且可重新生成。
- `*.exe` 是编译产物，不应进入源码提交历史。
- 日志可能包含本机任务名称、脚本路径等运行信息，不应上传。

## 3. 本地自检后提交源码

在项目根目录执行：

```powershell
cd "E:\work\Scheduled task"

# 查看待提交文件，确认没有 exe、日志或 .runtime
git status --short

# 运行测试
.\.runtime\go\bin\go.exe test ./...

# 暂存受 .gitignore 保护后的源码和文档
git add .

# 检查暂存内容
git diff --cached --stat

# 创建提交
git commit -m "feat: 描述本次修改"
```

如果项目目录没有 `.runtime\go\bin\go.exe`，可使用本机已安装的 Go 1.20：

```powershell
go test ./...
```

## 4. 推送到远端仓库（需要时）

仅在确认需要上传时执行：

```powershell
git push origin main
```

推送前建议先同步远端：

```powershell
git pull --rebase origin main
git push origin main
```

如果发生冲突，先解决冲突、运行测试、创建解决冲突的提交，再推送。不要使用 `git push --force` 覆盖远端历史。

## 5. 发布给普通用户

普通用户不需要 Go 源码或 `.runtime`。建议通过 GitHub/GitLab Release 上传：

```text
TaskSchedulerManager.exe
README 或使用说明
版本变更说明
```

发布前在干净环境或测试服务器上验证：

```powershell
.\TaskSchedulerManager-debug.exe --self-test
```

## 6. 发布前检查清单

- [ ] `go test ./...` 通过。
- [ ] `build.bat` 成功生成正式版和调试版。
- [ ] `git status --short` 中没有意外文件。
- [ ] `git diff --cached --stat` 中没有 exe、日志或 `.runtime`。
- [ ] README 与功能说明已更新。
- [ ] Release 附件只包含面向用户的 exe 和说明文件。
