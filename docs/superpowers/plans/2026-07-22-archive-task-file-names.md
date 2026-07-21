# Archive Task File Names Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Name every task XML entry in an export or backup ZIP from its flattened task path instead of a numeric sequence.

**Architecture:** Add pure filename normalization and uniqueness helpers in `task_archive.go`. `writeTaskArchive` will use the helpers when assigning the existing manifest `file` values; the ZIP reader remains unchanged because it already resolves entries through those manifest paths.

**Tech Stack:** Go 1.20.14; standard library `archive/zip`, `strings`, `unicode`, `testing`.

## Global Constraints

- Target Windows Server 2008 R2 and keep the project on Go 1.20.14.
- XML entries remain under `tasks/` and ZIP entry separators remain `/`.
- Do not change ZIP format version, manifest fields, XML content, import collision handling, or backup scope.
- Existing ZIP packages whose manifest points to `tasks/0001.xml` must remain importable.
- Import must restore the original task path from XML/manifest, never infer it from the readable filename.

---

### Task 1: Generate stable, safe flattened XML filenames

**Files:**
- Modify: `task_archive_test.go`
- Modify: `task_archive.go:79-151`

**Interfaces:**
- Consumes: `TaskArchiveItem.Name string` as a normalized Windows Task Scheduler path.
- Produces: `archiveTaskXMLFileName(name string) string` and `uniqueArchiveTaskXMLFileName(name string, used map[string]bool) string`.
- Uses: `writeTaskArchive(outputPath, source string, items []TaskArchiveItem) error` to put the returned name into `TaskArchiveManifestEntry.File`.

- [ ] **Step 1: Write failing filename tests**

Add this test to `task_archive_test.go`:

```go
func TestArchiveTaskXMLFileNameFlattensTaskPath(t *testing.T) {
	if got := archiveTaskXMLFileName(`\DouyinUser\DouyinGuard\LaunchDouyinGuard`); got != "DouyinUser_DouyinGuard_LaunchDouyinGuard.xml" {
		t.Fatalf("archiveTaskXMLFileName() = %q", got)
	}
	if got := archiveTaskXMLFileName(`\A:B*?`); got != "A_B__.xml" {
		t.Fatalf("archiveTaskXMLFileName() sanitization = %q", got)
	}
}

func TestUniqueArchiveTaskXMLFileNameAvoidsFlattenedCollisions(t *testing.T) {
	used := map[string]bool{}
	first := uniqueArchiveTaskXMLFileName(`\A\B`, used)
	second := uniqueArchiveTaskXMLFileName(`\A_B`, used)
	if first != "A_B.xml" || second != "A_B_2.xml" {
		t.Fatalf("got %q and %q", first, second)
	}
}
```

Also extend `TestTaskArchiveRoundTripKeepsTaskNameAndXML` to open the ZIP and assert that `manifest.json` records `tasks/Folder_Daily.xml`.

- [ ] **Step 2: Run focused tests and verify they fail**

Run:

```powershell
$env:GOROOT=(Resolve-Path '.runtime\go').Path
$env:GOPATH=(Resolve-Path '.runtime\gopath').Path
$env:GOCACHE=(Resolve-Path '.runtime\gocache-build').Path
$env:PATH="$env:GOROOT\bin;$env:PATH"
$env:GOPROXY='off'
& "$env:GOROOT\bin\go.exe" test -run 'TestArchiveTaskXMLFileNameFlattensTaskPath|TestUniqueArchiveTaskXMLFileNameAvoidsFlattenedCollisions|TestTaskArchiveRoundTripKeepsTaskNameAndXML' -count=1
```

Expected: fail because the filename helper functions do not exist and the manifest still uses `tasks/0001.xml`.

- [ ] **Step 3: Implement normalization, collision handling, and ZIP integration**

Add these functions to `task_archive.go`:

```go
func archiveTaskXMLFileName(name string) string {
	name = strings.TrimPrefix(strings.TrimSpace(name), `\`)
	name = strings.ReplaceAll(name, `\`, "_")
	var out strings.Builder
	for _, r := range name {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\\|?*`, r) {
			out.WriteByte('_')
			continue
		}
		out.WriteRune(r)
	}
	stem := strings.Trim(out.String(), " .")
	if stem == "" {
		stem = "task"
	}
	return stem + ".xml"
}

func uniqueArchiveTaskXMLFileName(name string, used map[string]bool) string {
	filename := archiveTaskXMLFileName(name)
	stem := strings.TrimSuffix(filename, ".xml")
	for suffix := 2; used[strings.ToLower(filename)]; suffix++ {
		filename = fmt.Sprintf("%s_%d.xml", stem, suffix)
	}
	used[strings.ToLower(filename)] = true
	return filename
}
```

In `writeTaskArchive`, create `usedFiles := make(map[string]bool, len(items))` before the item loop and replace the numeric filename assignment with:

```go
filename := "tasks/" + uniqueArchiveTaskXMLFileName(name, usedFiles)
```

- [ ] **Step 4: Run focused tests and verify they pass**

Run the command from Step 2.

Expected: `PASS`; the archive manifest uses the readable flattened filename and round-trip recovery still preserves `\Folder\Daily`.

- [ ] **Step 5: Run regression suite and build both EXE variants**

Run:

```powershell
& "$env:GOROOT\bin\gofmt.exe" -w task_archive.go task_archive_test.go
& "$env:GOROOT\bin\go.exe" test -count=1 ./...
& "$env:GOROOT\bin\go.exe" build -trimpath -ldflags='-H windowsgui' -o TaskSchedulerManager.exe .
& "$env:GOROOT\bin\go.exe" build -trimpath -o TaskSchedulerManager-debug.exe .
```

Expected: all tests pass and both binaries build.

- [ ] **Step 6: Commit the implementation**

```powershell
git add -- task_archive.go task_archive_test.go
git commit -m "feat: name archive XML files after tasks"
```
