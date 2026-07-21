# 定时任务导入、导出与备份 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** 为 Windows Server 2008 R2 定时任务管理器增加批量导入、按选中或筛选结果导出、以及全量 ZIP 备份能力。

**Architecture:** 用标准库 archive/zip 将原生任务 XML 与 manifest.json 封装为离线备份包。归档读写、XML 名称提取和导入计划放入独立 Go 文件；Win32 界面仅负责选择文件、预检、冲突确认和后台状态展示。

**Tech Stack:** Go 1.20.14、archive/zip、crypto/sha256、encoding/json、encoding/xml、schtasks.exe、github.com/lxn/win。

## Global Constraints

- 兼容 Windows Server 2008 R2，且不依赖联网服务。
- 备份包只保存任务 XML 与清单，不复制脚本、程序或任务执行数据。
- XML 必须复用 exportTaskXML 导出和 restoreTaskXML 恢复。
- 同名任务逐条由用户选择覆盖或跳过，禁止静默覆盖。
- 导入、导出、备份操作不能阻塞 UI 线程；外部命令继续使用既有超时机制。

---

### Task 1: 创建 ZIP 归档核心

**Files:**
- Create: task_archive.go
- Create: task_archive_test.go

**Interfaces:**
- TaskArchiveItem contains Name and XML.
- TaskArchiveManifest contains FormatVersion, ExportedAt, Source, and Tasks.
- writeTaskArchive(path, source, items) writes manifest.json and tasks/0001.xml entries.
- readTaskArchive(path) validates and returns items in manifest order.
- taskNameFromXML(xmlText) reads RegistrationInfo.URI and normalizes it.

- [ ] **Step 1: Write failing tests**

Add tests that write one task to a temp ZIP, read it back, and assert the task name/XML are identical. Add malformed archive fixtures for traversal names, mismatched SHA-256, duplicate entries, missing XML, and URI/name mismatch.

- [ ] **Step 2: Verify RED**

Run: .runtime/go/bin/go.exe test -run TestTaskArchive -count=1

Expected: compile failure because archive types and functions are absent.

- [ ] **Step 3: Implement minimal archive support**

Use archive/zip and encoding/json. Write XML bytes with a lowercase SHA-256 entry in manifest.json. Reject absolute paths, dot-dot components, non-tasks paths, duplicate names/files, unsupported format versions, checksum mismatches, malformed XML, and a task URI different from the manifest name.

- [ ] **Step 4: Verify GREEN**

Run: .runtime/go/bin/go.exe test -run TestTaskArchive -count=1; .runtime/go/bin/go.exe test -count=1 ./...

- [ ] **Step 5: Commit**

Run: git add task_archive.go task_archive_test.go; git commit -m feat-add-task-archive-format

### Task 2: 创建批量传输的纯函数

**Files:**
- Modify: task_archive.go
- Modify: task_archive_test.go

**Interfaces:**
- TaskTransferResult contains Name, Skipped, and Err.
- collectTaskArchiveItems(tasks, exporter) returns successful XML items and one result per task.
- TaskImportPlanItem contains Item, Exists, Overwrite, and Skip.
- buildTaskImportPlan(items, existing) marks exact normalized existing names.
- applyTaskImportPlan(plan, restorer) continues after individual restore failures.

- [ ] **Step 1: Write failing tests**

Test that a failed XML export does not discard later successful tasks. Test that skipped items never call the restorer and that a failed restore does not block a later task. Test that an existing task is marked as a collision.

- [ ] **Step 2: Verify RED**

Run: .runtime/go/bin/go.exe test -run TestCollectTaskArchiveItems -count=1

Expected: compile failure because transfer helper functions are absent.

- [ ] **Step 3: Implement minimal helpers**

Call the injected exporter once per task, retain individual errors, normalize successful XML, and return every result. For import, call the injected restorer only for non-skipped plan entries and continue after errors.

- [ ] **Step 4: Verify GREEN and commit**

Run: .runtime/go/bin/go.exe test -count=1 ./...

Run: git add task_archive.go task_archive_test.go; git commit -m feat-add-batch-task-transfer-helpers

### Task 3: 接入后台导出、备份与导入流程

**Files:**
- Modify: main.go
- Modify: task_archive.go
- Modify: task_archive_test.go

**Interfaces:**
- exportTasksAsync(tasks, path, title) runs collection and ZIP writing in a goroutine.
- startImportPreflight(path) parses ZIP/XML and posts a UI completion message.
- pendingImportItems, pendingTransferResults, and WM_APP messages carry worker results to the UI thread.

- [ ] **Step 1: Write failing scope test**

Add a tasksForExport(selected, filtered) test proving one selected task wins; nil selection returns a copy of all filtered tasks.

- [ ] **Step 2: Verify RED**

Run: .runtime/go/bin/go.exe test -run TestTasksForExportUsesSelectionOtherwiseFiltered -count=1

Expected: compile failure because tasksForExport is absent.

- [ ] **Step 3: Implement orchestration**

Add import/export/backup operation state and WM_APP messages. Export uses the selected item or current viewTasks. Backup uses a copy of allTasks and defaults to TaskBackup_yyyyMMdd_HHmmss.zip. Start all XML export and restore calls in goroutines. A partial archive writes successful tasks and reports each failed item. For a standalone XML, read the file and use taskNameFromXML; for ZIP use readTaskArchive.

On UI-thread import preflight, display count and command summary. For every collision use MB_YESNOCANCEL: yes overwrite, no skip, cancel aborts before any writes. Then execute the final plan in a goroutine through restoreTaskXML and refresh only when it completes.

- [ ] **Step 4: Verify GREEN and commit**

Run: .runtime/go/bin/gofmt.exe -w main.go task_archive.go task_archive_test.go; .runtime/go/bin/go.exe test -count=1 ./...

Run: git add main.go task_archive.go task_archive_test.go; git commit -m feat-add-task-archive-operations

### Task 4: 接入界面与发布验证

**Files:**
- Modify: main.go
- Modify: README.md

- [ ] **Step 1: Implement controls**

Create top-toolbar buttons 导入, 导出, 备份全部 before 刷新 and 新建任务. At minimum window width position backup at width minus 522, export at width minus 426, import at width minus 330, refresh at width minus 202, and create at width minus 118. Disable these buttons whenever operationBusy or loadingTasks is true, including watchdog recovery.

- [ ] **Step 2: Implement file dialogs and status messages**

Use existing runExternalPicker conventions: import filter includes ZIP and XML; export/backup use a ZIP save dialog. Cancellation must not alter application state. Completion messages include success, skipped, and failed counts plus failed task names/reasons.

- [ ] **Step 3: Update docs**

Document ZIP/XML support, backup exclusions, SYSTEM import elevation requirement, same-name overwrite/skip prompts, and password-protected user tasks that cannot be restored without credentials.

- [ ] **Step 4: Verify release artifacts**

Run: .runtime/go/bin/gofmt.exe -w main.go main_test.go task_archive.go task_archive_test.go

Run: .runtime/go/bin/go.exe test -count=1 ./...

Run: .runtime/go/bin/go.exe build -buildvcs=false -trimpath -ldflags=-H windowsgui -o TaskSchedulerManager.exe .

Run: .runtime/go/bin/go.exe build -buildvcs=false -trimpath -o TaskSchedulerManager-debug.exe .

- [ ] **Step 5: Commit**

Run: git add main.go main_test.go README.md TaskSchedulerManager.exe TaskSchedulerManager-debug.exe; git commit -m feat-expose-task-archive-management
