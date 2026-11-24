# Spec Delta

## Purpose

提供 Windows 定时任务的生命周期全链路日志追踪能力，包括任务创建时间的解析与展示、基于系统底层事件日志的执行历史流查询，以及对管理器自身运维操作的轻量化本地结构化审计记录。

## ADDED Requirements

### Requirement: 任务创建时间解析与展示
系统 SHALL 从任务计划程序的原生 XML 定义中的 `RegistrationInfo.Date` 字段解析任务创建时间，并在主界面任务列表及任务详情面板中展示该时间。

#### Scenario: 成功解析并展示任务创建时间
- **WHEN** 任务计划程序中的任务 XML 包含有效的 `RegistrationInfo.Date` 字段（如 `2026-07-21T10:30:00`）
- **THEN** 系统解析该 ISO-8601 字符串并格式化为标准时间格式，在主列表“创建时间”列和详情视图中正确展示

#### Scenario: 任务 XML 缺少创建时间字段
- **WHEN** 某些旧版系统内置任务或精简任务 XML 未包含 `RegistrationInfo.Date` 节点
- **THEN** 系统将其优雅显示为“-”或未知，不阻塞任务列表加载及正常管理操作

### Requirement: 基于事件日志的任务执行历史查询
系统 SHALL 针对选中的指定任务，通过 Windows 事件日志通道（`Microsoft-Windows-TaskScheduler/Operational`）查询该任务最近的执行历史记录，提取开始时间、完成时间、执行耗时与进程退出代码。

#### Scenario: 查询任务最近执行历史
- **WHEN** 用户在主界面选中某个任务并切换至“运行历史”面板
- **THEN** 系统在后台异步查询该任务对应的 Event ID 100（任务启动）和 Event ID 102/201（任务完成/操作完成），配对计算后在列表中呈现最新的执行记录

#### Scenario: 任务历史上报错误状态码
- **WHEN** 某次执行的操作返回了非零退出代码（如 0x1 或 0xC0000005）
- **THEN** 运行历史列表中将该次记录的状态明确标记为“失败”，并展示具体退出码数值

### Requirement: 系统调度历史通道状态检测与自愈
系统 SHALL 检测当前 Windows 系统的 `Microsoft-Windows-TaskScheduler/Operational` 日志通道是否处于启用状态；若未启用，系统 SHALL 提供友好的状态提示与一键启用入口。

#### Scenario: 检测到日志通道已启用
- **WHEN** 用户打开运行历史面板且系统通道 `enabled: true`
- **THEN** 系统正常加载并展示任务执行历史记录列表

#### Scenario: 检测到日志通道未启用并触发一键启用
- **WHEN** 系统通道处于禁用状态
- **THEN** 界面显示醒目的提示信息与“启用执行历史记录”按钮，用户点击后以管理员权限调用系统命令开启通道

### Requirement: 本地管理运维操作审计流
系统 SHALL 将本管理工具内执行的创建、修改、删除、启动、停止、导入等管理操作，以 JSON Lines 格式异步追加记录至本地 `logs/audit.jsonl` 文件中。

#### Scenario: 手动触发或修改任务时写入审计日志
- **WHEN** 运维人员通过本工具创建新任务、修改配置或点击“运行”按钮
- **THEN** 系统异步向 `logs/audit.jsonl` 追加一条包含操作时间戳、操作人、动作类型、目标任务路径和执行结果的 JSON 记录，且不阻塞主 UI 线程响应
