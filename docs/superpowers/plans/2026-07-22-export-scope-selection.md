# Export Scope Selection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Export command export the current filtered task set unless the user has explicitly selected one task.

**Architecture:** Keep the existing `tasksForExport(selected, filtered)` policy and remove implicit selection from list rendering. A small pure policy helper will make the no-default-selection rule unit-testable; the ListView and detail panel consume that policy without changing the archive format or background export pipeline.

**Tech Stack:** Go 1.20.14, Win32 ListView (`github.com/lxn/win`), Go standard `testing`.

## Global Constraints

- Target Windows Server 2008 R2 and keep the project on Go 1.20.14.
- Do not add UI buttons or alter ZIP/XML import and backup formats.
- `备份全部` must remain independent of filters and task selection.
- No selected task means the task-action buttons are disabled and the detail panel shows the existing guidance text.

---

### Task 1: Remove implicit task selection after list rendering

**Files:**
- Modify: `main_test.go`
- Modify: `main.go:921-978`

**Interfaces:**
- Consumes: `viewTasks []Task`, `tasksForExport(selected *Task, filtered []Task) []Task`.
- Produces: `defaultTaskSelectionIndex(taskCount int) int`, where `-1` means no automatic selection.

- [ ] **Step 1: Write the failing tests**

Add these tests in `main_test.go`:

```go
func TestDefaultTaskSelectionIndexDoesNotSelectAfterRender(t *testing.T) {
	for _, count := range []int{0, 1, 3} {
		if got := defaultTaskSelectionIndex(count); got != -1 {
			t.Fatalf("defaultTaskSelectionIndex(%d) = %d, want -1", count, got)
		}
	}
}

func TestTasksForExportWithoutSelectionKeepsAllFilteredTasks(t *testing.T) {
	filtered := []Task{{Name: `\A`}, {Name: `\B`}, {Name: `\C`}}
	got := tasksForExport(nil, filtered)
	if len(got) != len(filtered) {
		t.Fatalf("tasksForExport(nil, filtered) returned %d tasks, want %d", len(got), len(filtered))
	}
}
```

- [ ] **Step 2: Run the focused tests and verify the expected failure**

Run:

```powershell
$env:GOROOT=(Resolve-Path '.runtime\go').Path
$env:GOPATH=(Resolve-Path '.runtime\gopath').Path
$env:GOCACHE=(Resolve-Path '.runtime\gocache-build').Path
$env:PATH="$env:GOROOT\bin;$env:PATH"
$env:GOPROXY='off'
& "$env:GOROOT\bin\go.exe" test -run 'TestDefaultTaskSelectionIndexDoesNotSelectAfterRender|TestTasksForExportWithoutSelectionKeepsAllFilteredTasks' -count=1
```

Expected: fail because `defaultTaskSelectionIndex` is not defined.

- [ ] **Step 3: Implement the minimal selection policy and use it in rendering**

Add to `main.go`:

```go
func defaultTaskSelectionIndex(taskCount int) int {
	return -1
}
```

Replace the automatic first-row selection in `renderList` with:

```go
if index := defaultTaskSelectionIndex(len(viewTasks)); index >= 0 && index < len(viewTasks) {
	item := win.LVITEM{IItem: int32(index), StateMask: win.LVIS_SELECTED, State: win.LVIS_SELECTED}
	win.SendMessage(listView, win.LVM_SETITEMSTATE, uintptr(index), uintptr(unsafe.Pointer(&item)))
	updateTaskDetail(viewTasks[index])
	return
}
updateTaskDetail()
```

Remove the `len(viewTasks) == 1` branch in `updateTaskDetail`, so a one-row filter does not become an implicit selection:

```go
if len(tasks) == 0 {
	setWindowText(detailEdit, "选择一条任务后，这里会显示完整配置。\r\n\r\n提示：浅绿色表示正在运行，灰色表示已禁用。")
	updateActionButtons(nil)
	return
}
```

- [ ] **Step 4: Run focused tests and verify they pass**

Run the command from Step 2.

Expected: `PASS`.

- [ ] **Step 5: Run regression tests and build both executables**

Run:

```powershell
& "$env:GOROOT\bin\gofmt.exe" -w main.go main_test.go
& "$env:GOROOT\bin\go.exe" test -count=1 ./...
& "$env:GOROOT\bin\go.exe" build -trimpath -ldflags='-H windowsgui' -o TaskSchedulerManager.exe .
& "$env:GOROOT\bin\go.exe" build -trimpath -o TaskSchedulerManager-debug.exe .
```

Expected: tests pass and both EXE files build successfully.

- [ ] **Step 6: Commit the implementation**

```powershell
git add -- main.go main_test.go TaskSchedulerManager.exe TaskSchedulerManager-debug.exe
git commit -m "fix: export filtered tasks without explicit selection"
```
