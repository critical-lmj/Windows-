# Windows 定时任务管理器 Go 版项目介绍

## 项目概述

`go-task-scheduler-manager` 是一个面向 Windows 的本地桌面端定时任务管理工具，使用 Go 语言和 Win32 API 实现图形界面，不依赖浏览器和联网服务。

项目目标是提供一个比命令行更直观、比系统任务计划程序更轻量的管理界面，用于查看、筛选、创建和管理本机 Windows 任务计划程序中的定时任务。

目标运行环境：

- Windows 11 测试环境
- Windows Server 2008 R2 Enterprise 部署环境
- 离线运行
- 可携带 Go 运行时进行离线构建

## 技术方案

项目采用纯 Go 桌面程序方案：

- GUI：使用 `github.com/lxn/win` 调用 Win32 API 创建原生窗口。
- 任务读取：调用系统自带 `schtasks.exe /query /fo LIST /v`（异常时自动回退至 PowerShell COM 接口兜底）。
- 任务管理：调用 `schtasks.exe` 执行创建、运行、停止、启用、禁用、删除，并支持 ZIP/XML 批量备份导入导出。
- 独立运行历史采集：内置/守护采集器定时轮询全部定时任务状态与 `LastRunTime`/`LastResult` 变化，完全不读取系统自带事件日志，按天独立保存至 `logs/exec_YYYY-MM-DD.jsonl`。
- 后台常驻与开机自启：支持 `--daemon` 无界面后台守护模式（`SYSTEM / ONSTART` 服务器无人登录自启）与纯 Win32 系统托盘常驻（关闭窗口自动隐藏至托盘），通过全局命名互斥锁防止重复采集。
- 本地审计流：通过异步缓冲通道将运维管理操作写入按月拆分的 `logs/audit_YYYY-MM.jsonl`，并将单任务审计标记持久化至 `logs/audit_policy.json`。
- 编码兼容：对 `schtasks.exe` 中文输出进行 ANSI/系统代码页解码，CSV 导出统一写入 UTF-8 BOM，避免中文 Windows 与 Excel 乱码。
- 权限处理：正式版启动时会自动请求管理员权限，以便读取和管理受保护任务。
- 离线构建：项目内可放置 `.runtime\go` 便携 Go 环境，`build.bat` 优先使用本地 Go。

## 主要功能

### 任务列表可视化

程序启动后读取本机 Windows 任务计划程序中的任务，并以表格展示：

- 任务名称
- 状态
- 日志审计（已开启(默认) / 已开启(手动) / 未开启(系统默认) / 已关闭(手动)）
- 下次运行时间
- 上次运行时间
- 上次结果
- 创建时间（解析自任务 XML `RegistrationInfo.Date`）
- 计划类型
- 任务位置
- 执行命令
- 详情面板额外展示：任务分类、脚本路径、起始目录、任务完整路径与描述

其中：

- `任务路径` 表示任务计划程序内部路径，例如 `\taxt php log`。
- `脚本路径` 会根据执行命令和起始目录推导脚本实际路径，例如 `E:\work\scheduled_task\write_md5_log.php`。
- `起始目录` 对应系统任务计划程序中的“起始于”。

### 筛选查询

支持按关键字筛选任务，并可选择筛选范围：

- 全部字段
- 名称
- 描述
- 位置
- 执行路径

同时支持状态与分类筛选：

- 全部状态 / 仅启用 / 仅禁用
- 仅自定义任务 / 仅系统任务 / 仅已开审计

适合按任务名、任务描述、任务目录、exe 路径、脚本路径及任务归属快速定位任务。

### 独立任务运行日志、后台常驻与 CSV 导出

右侧面板提供 `详情`、`运行历史`、`操作审计` 三个视图以及 `开机自启`、`导出日志` 快捷入口：

- **独立按天运行留痕（不读取系统自带日志）**：后台采集器默认每 10 秒轮询本机全部定时任务（覆盖自定义任务与系统级任务），基于 `logs/monitor_state.json` 状态基线比对增量变化，自动捕获启动、结束、估算耗时与退出代码，按自然天拆分写入 `logs/exec_YYYY-MM-DD.jsonl`。读取时跨天倒序聚合，并对同一 `InstanceID` 自动合并保留最终状态。
- **开机自启与系统托盘常驻**：
  - 点击 **`开机自启`** 可一键注册 `SYSTEM / ONSTART` 后台静默守护任务（`--daemon`）及登录托盘静默任务（`--minimized`），确保服务器无人登录重启也能持续记录日志。
  - 点击主窗口关闭按钮自动隐藏到右下角系统托盘常驻；托盘右键菜单支持显示主窗口、切换开机自启与彻底退出。
  - 守护进程与桌面 GUI 通过 Windows 全局命名互斥锁协调，保证同一时间仅有一个采集器运行。
- **按月操作审计流**：管理操作（创建、修改、删除、启停、导入导出、开启/关闭开机自启等）异步落盘至 `logs/audit_YYYY-MM.jsonl`，按自然月自动滚动拆分；查看与导出时自动跨月倒序聚合读取，并兼容旧版 `logs/audit.jsonl`。
- **自定义条数 CSV 导出**：点击 `导出日志` 先弹出条数设置窗口（默认 `200`，提示输入 `0` 或 `-1` 表示不限制数量导出全部），导出文件统一带 UTF-8 BOM 防止 Excel 乱码。

### 任务管理

支持对选中的任务执行：

- 刷新
- 新建任务
- 修改基础任务和安全重命名
- 运行任务
- 停止任务
- 启用任务
- 禁用任务
- 删除任务

删除任务前会弹出确认提示。

修改任务时会从 XML 回读原配置。任务名称发生变化时，程序先创建并验证新任务，再删除旧任务；选择执行文件不会覆盖用户填写的任务名称。多触发器、多操作或高级重复任务会提示使用系统任务计划程序修改。

### 新建任务

新建任务窗口支持配置：

- 任务名
- 描述
- 触发器
- 开始日期
- 时间点
- 执行文件
- 参数
- 起始目录

触发器支持基础计划：

- 一次
- 每天
- 每周
- 每月

每周任务可勾选星期几，每月任务可选择每月日期或最后一天。程序会自动转换为 `schtasks.exe` 参数，避免用户手写 `MON,WED`、`15` 等规则。

### 文件和目录选择

执行文件支持选择：

- `.exe`
- `.bat`
- `.cmd`
- `.ps1`
- `.vbs`
- `.js`
- 其他文件

起始目录单独选择文件夹，不会因为选择执行文件而自动填写，避免 `php.exe + 脚本` 这种场景被错误设置为 PHP 程序目录。

## 兼容性设计

项目面向 Windows Server 2008 R2 Enterprise，因此采用较保守的实现方式：

- 使用 Go 1.20.14，避免 Go 1.21 及以上版本不再支持 Server 2008 R2 的问题。
- 使用 Win32 原生窗口，避免 WebView、浏览器内核等额外依赖。
- 使用系统自带 `schtasks.exe`，避免引入复杂的 COM 封装依赖。
- 构建脚本支持便携 Go，便于复制到新机器后离线构建。

## 目录说明

项目目录：

```text
E:\work\go-task-scheduler-manager
```

主要文件：

```text
main.go                         主程序源码（Win32 界面与任务管理）
main_test.go                    主程序与筛选/导出解析单元测试
task_archive.go                 任务 ZIP 备份与 XML 导入导出
task_archive_test.go            归档与导入导出单元测试
task_audit.go                   按月操作审计流与单任务审计标记持久化
task_audit_test.go              审计日志拆分与策略单元测试
task_exec_monitor.go            独立后台按天运行日志采集器与状态机
task_exec_monitor_test.go       按天日志拆分、去重与轮询 diff 测试
autostart.go                    全局互斥锁、--daemon 守护模式与开机自启
autostart_test.go               启动参数、自启命令与互斥锁测试
tray.go                         纯 Win32 系统托盘常驻与右键菜单
task_eventlog.go                运行历史数据结构与 CSV 导出
task_eventlog_test.go           历史记录 CSV 导出与兼容性测试
openspec/                       OpenSpec 需求规格与变更归档
build.bat                       构建正式版和调试版
prepare-portable-go.bat         准备便携 Go 环境
TaskSchedulerManager.exe        正式桌面程序（不提交源码库，通过 Release 发布）
TaskSchedulerManager-debug.exe  调试版程序（不提交源码库）
README.md                       基础说明
权限和筛选说明.md               权限、筛选、触发器说明
docs/Git上传清单与发布指南.md   Git 提交流程与 Release 发布指南
logs/exec_YYYY-MM-DD.jsonl      按天拆分的全部任务独立运行日志（运行期生成，已忽略）
logs/monitor_state.json         后台采集器任务状态基线（运行期生成，已忽略）
logs/audit_YYYY-MM.jsonl        按月拆分的操作审计日志（运行期生成，已忽略）
logs/audit_policy.json          单任务手动审计标记配置（运行期生成，已忽略）
.runtime\go                     便携 Go 运行时（已忽略）
.runtime\gopath                 Go modules 缓存（已忽略）
debug.log                       程序调试日志（已忽略）
```

## 构建方式

如果 `.runtime\go` 已存在，可直接运行：

```bat
build.bat
```

构建输出：

```text
TaskSchedulerManager.exe
TaskSchedulerManager-debug.exe
```

调试版可执行自测：

```bat
.\TaskSchedulerManager-debug.exe --self-test
```

自测会依次创建、查询并删除“一次、每天、每周、每月、关机事件”五种临时任务，用于验证全部基础计划类型。任务使用 `\CodexScheduleSelfTest_` 前缀，并会自动清理。

## 权限说明

Windows 任务计划程序中部分任务需要管理员权限才能读取或管理。正式版 `TaskSchedulerManager.exe` 启动时会自动请求管理员权限。

如果取消管理员权限，程序仍可打开，但可能出现：

- 某些系统任务读取不到
- 某些任务无法运行、禁用、删除
- `schtasks.exe` 返回 `Access is denied`

## 当前限制

当前版本聚焦常用定时任务管理，暂未完整覆盖系统任务计划程序的全部高级配置。

暂未可视化支持：

- 随机延迟
- 到期日期
- 多触发器
- 多操作
- 条件页高级设置
- 设置页高级设置

如果后续需要完整复刻系统任务计划程序，建议改为 XML 注册任务方式，以支持更多高级字段。

## 适用场景

适合用于：

- Windows 服务器本地定时任务巡检
- 快速查看脚本任务执行路径
- 管理 PHP、BAT、CMD、PowerShell 等脚本定时任务
- 离线服务器环境部署
- Windows Server 2008 R2 上轻量化任务管理

## 项目定位

本项目不是完整替代 Windows 自带任务计划程序，而是一个更轻量、更聚焦脚本任务管理的桌面工具。

它重点解决：

- 任务太多时难以搜索
- 执行命令和脚本路径不直观
- 普通用户不熟悉 `schtasks.exe`
- 离线服务器不方便安装复杂依赖
