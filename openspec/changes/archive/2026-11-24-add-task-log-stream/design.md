# Design

## Context

项目基于 Go 1.20 和 Win32 原生 API 开发，运行于 Windows Server 2008 R2 及更高版本环境。由于任务计划程序调度运行在 Windows 核心服务中，任务往往在无人值守时执行，无法由 GUI 客户端驻留实时监听。方案需在零外部依赖、离线环境的前提下，提取任务历史执行记录与操作痕迹。详见 `proposal.md`。

## Goals / Non-Goals

**Goals:**
- 实现任务 XML 中 `RegistrationInfo.Date` 字段提取并在主表展示创建时间。
- 实现基于系统自带 `wevtutil.exe` 查询 `Microsoft-Windows-TaskScheduler/Operational` 事件日志，并解析为结构化执行流（起止时间、耗时、退出码）。
- 支持日志通道启用状态检测与提权一键开启。
- 实现非阻塞的本地操作审计追加日志（`logs/audit.jsonl`）。
- 保持对 Windows Server 2008 R2 Enterprise 的纯离线向下兼容。

**Non-Goals:**
- 不引入外部关系型或时序数据库（如 SQLite、MySQL 等），避免 CGO 编译依赖。
- 不构建常驻 Windows 后台服务/守护进程，依靠 Windows 原生事件持久化。
- 不提供跨网络服务器的分布式日志汇聚功能。

## Decisions

### 决策 1：采用 wevtutil.exe 作为事件日志采集载体
- **选择**：调用系统自带 `wevtutil.exe qe "Microsoft-Windows-TaskScheduler/Operational" ... /f:XML /rd:true /c:50`。
- **理由**：
  - `wevtutil.exe` 自 Windows Vista 和 Windows Server 2008 起即为系统内置核心命令，完全无外部依赖。
  - 相比 PowerShell `Get-WinEvent`，`wevtutil.exe` 启动速度更快，内存开销更低，在 2008 R2 上兼容性更稳固。
  - 限制 `/c:50`，保证快速响应，避免大规模日志造成内存冲击。
- **替代方案考虑**：
  - *方案 B：Windows EventLog C API (`EvtQuery`)*：需要使用系统 DLL 导入或 CGO，代码复杂度高，易在 Go 1.20 / Win32 消息循环中引发线程问题。
  - *方案 C：自行驻留后台监听*：需安装并维护 Windows 服务，运维成本极高，违背轻量化设计初衷。

### 决策 2：任务创建时间读取机制
- **选择**：解析任务原生 XML 中的 `<RegistrationInfo><Date>` 节点。
- **理由**：Windows 任务计划程序原生持久化该属性，无需本地额外维护对照表；老任务也能直接读取。

### 决策 3：本地审计日志采用 Channel 异步追加写入 JSON Lines
- **选择**：`logs/audit.jsonl` + 单一 Worker 协程从缓冲 Channel 消费写入。
- **理由**：JSON Lines 便于行级读取与解析，单条记录体积小（< 1KB）；异步 Channel 彻底解耦 UI 线程，即使磁盘 I/O 偶发阻塞也不影响界面响应。

## Risks / Trade-offs

- **[风险] 某些纯净服务器默认禁用 `Operational` 日志通道**
  - $\rightarrow$ 缓解措施：在界面提供通道自检状态提示，并提供一键开启按钮，复用现有管理员提权执行 `wevtutil sl ... /e:true`。
- **[风险] 执行历史较长或日志量巨大导致查询超时**
  - $\rightarrow$ 缓解措施：强制限制查询条数（最多 50 条），并为 `wevtutil` 设置 5 秒严格超时控制（通过 `runCommandWithTimeout`），超时后自动结束进程树。
- **[风险] 任务并发启动导致 Event ID 100 与 102 乱序**
  - $\rightarrow$ 缓解措施：通过事件中的 `InstanceId` (GUID) 关联启动与完成事件，精确计算单次调度的耗时。
