# 任务名称重复校验实施计划

> **For Codex:** 实施时按 `superpowers:executing-plans` 逐项执行，并在每项完成后记录验证结果。

**目标：** 在新建和修改任务保存前，利用已加载的任务列表即时校验任务名称是否重复；保留保存子进程中的最终校验，以避免并发或列表过期导致重名。

**设计：** 增加一个不访问系统、不启动外部进程的纯函数，统一处理名称规范化、非法名称和大小写不敏感的重复判断。创建/修改向导在开始异步保存前调用该函数；名称重复或非法时提示用户并将输入焦点返回任务名称框。现有 `saveTaskConfig` 中的 `taskExists` 校验继续作为最终防线。

**技术栈：** Go 1.20、github.com/lxn/walk、Windows Task Scheduler（schtasks.exe）。

---

## 任务 1：保存前执行任务名称重复校验

**涉及文件：**

- 修改：`main_test.go`
- 修改：`main.go`

### 1. 先编写失败测试

在 `main_test.go` 新增 `TestValidateTaskNameAvailability`，覆盖：

- 新建任务时，名称与现有任务相同或仅大小写不同会被拒绝；
- 修改任务且名称保持不变时允许保存；
- 修改任务改名为另一现有任务的名称会被拒绝；
- 非法名称优先返回现有的名称格式错误，而不是重复错误。

运行：

```powershell
$env:GOROOT=(Resolve-Path '.runtime\go').Path
$env:GOPATH=(Resolve-Path '.runtime\gopath').Path
$env:GOCACHE=(Resolve-Path '.runtime\gocache-build').Path
$env:PATH="$env:GOROOT\bin;$env:PATH"
$env:GOPROXY='off'
& "$env:GOROOT\bin\go.exe" test -count=1 -run TestValidateTaskNameAvailability .
```

预期：测试因 `validateTaskNameAvailability` 尚未定义而失败。

### 2. 实现纯校验函数

在 `main.go` 的 `normalizeTaskName` 附近新增 `validateTaskNameAvailability(candidate, originalName string, tasks []Task) error`：

- 先调用 `normalizeTaskName(candidate)`，格式不合法时直接返回该错误；
- 对编辑场景规范化 `originalName`；
- 遍历 `tasks`，对任务名称规范化后以 `strings.EqualFold` 比较；
- 当前编辑的原任务本身不判为重复；
- 发现其他同名任务时返回明确的“任务名已存在，请换一个名称。”错误；
- 不调用 `schtasks.exe`、不读取 COM，确保 UI 线程不会因名称校验阻塞。

### 3. 接入创建/修改向导

在 `createBasicTask` 获取表单配置和原名称之后、设置 `wizardSaving` 与启动保存 goroutine 之前：

- 调用 `validateTaskNameAvailability(cfg.Name, originalName, allTasks)`；
- 出错时显示提示，将焦点设回 `wizardName` 并直接返回；
- 校验通过后保持当前的异步保存流程不变。

### 4. 格式化与验证

运行：

```powershell
$env:GOROOT=(Resolve-Path '.runtime\go').Path
$env:GOPATH=(Resolve-Path '.runtime\gopath').Path
$env:GOCACHE=(Resolve-Path '.runtime\gocache-build').Path
$env:PATH="$env:GOROOT\bin;$env:PATH"
$env:GOPROXY='off'
& "$env:GOROOT\bin\gofmt.exe" -w main.go main_test.go
& "$env:GOROOT\bin\go.exe" test -count=1 ./...
& "$env:GOROOT\bin\go.exe" build -trimpath -ldflags='-H windowsgui' -o TaskSchedulerManager.exe .
& "$env:GOROOT\bin\go.exe" build -trimpath -o TaskSchedulerManager-debug.exe .
```

预期：测试通过，正式版和调试版均能构建；保存成功路径仍由 `saveTaskConfig` 的系统端 `taskExists` 复核。

### 5. 提交变更

```powershell
git add main.go main_test.go
git commit -m "feat: validate duplicate task names before save"
```

只提交本功能相关的源代码和测试；不纳入工作区内已有的无关未跟踪文档。
