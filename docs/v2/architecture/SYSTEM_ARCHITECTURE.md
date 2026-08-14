# Visoraft V2 系统架构与实现方案

> 状态：提议架构（`proposed`），必须通过 Phase 0 PoC 才能进入实施。
> V2 尚未实现；本文中的目录、接口和表均为目标设计。

## 1. 架构结论

推荐采用三层结构：

1. **自研产品控制面**：Go + PostgreSQL，负责用户、连接、工作流草稿/版本、内容、审批、交付和计量。
2. **Temporal 持久执行面**：负责长时工作流、计时器、暂停/恢复、Activity 重试、事件历史和故障续跑。
3. **V1 兼容与媒体执行层**：复用现有任务、字幕、本地化、转码、发布和对象存储能力，通过稳定适配接口接入。

不推荐把 n8n 直接嵌入为商业产品内核。除媒体负载和安全面不匹配外，n8n 官方 Sustainable Use License 限制将其核心能力作为面向客户的商业产品；嵌入编辑器需要独立 OEM 商业条款。[n8n 许可说明](https://github.com/n8n-io/n8n-docs/blob/main/docs/privacy-and-security/sustainable-use-license.md)，[n8n OEM](https://n8n.io/oem/)

## 2. 为什么是 Temporal

V2 有大量持续数小时到数天的状态：大文件处理、平台排期、人工审批、连接恢复、速率限制、发布审核和未知结果回查。Temporal 官方把 Workflow Execution 定义为可持久、故障后可恢复的执行，并通过事件历史 replay 恢复；Activity 适合转码、外部 API 调用等动作，并支持用 heartbeat details 保存检查点。[Workflow Execution](https://docs.temporal.io/workflow-execution)，[Activities](https://docs.temporal.io/activities)

### PoC 必测项

- 读取不可变图定义并执行 10–20 个受约束节点。
- FFmpeg/下载 Activity 心跳、取消和断点恢复。
- 等待 48 小时审批、排期计时器和时区变更。
- 某一目标成功、另一目标失败后的部分成功恢复。
- 发布调用超时但远端可能成功时的 reconcile。
- Worker 升级和执行历史 replay 兼容。
- 运行成本、事件历史增长、可观测性和自托管/Cloud 运维成本。

Temporal 官方建议在问题规模有界时先保持单一 Workflow，以 Activity 为主，只有明确需要隔离服务或拆分历史时再使用 Child Workflow；V2 的普通工作流应遵守这一原则，V1 深度本地化复合动作再评估独立子工作流。[Child Workflows](https://docs.temporal.io/child-workflows)

## 3. 控制面与执行面职责

| 组件 | 职责 | 真相来源 |
| --- | --- | --- |
| Automation API | 工作流 CRUD、校验、发布、运行控制 | PostgreSQL |
| Connector Registry | 能力清单、版本、连接设置 schema | 代码 + 数据库投影 |
| Connection Service | OAuth、密钥、权限、健康和重连 | 加密凭证库 + PostgreSQL |
| Trigger Ingestor | webhook、push、poll、手动输入、游标 | PostgreSQL 去重记录 |
| Workflow Compiler | 静态图检查、能力匹配、执行计划 | 不可变 WorkflowVersion |
| Temporal Worker | 编排节点、等待、重试、暂停/取消 | Temporal Event History |
| Media Workers | 下载、FFprobe、字幕、FFmpeg、AI | S3 + NodeRun 结果 |
| Delivery Service | 发布、幂等、回查、删除、分析回填 | Delivery + Attempt |
| Approval Service | 审批步骤、期限、退回和通知 | PostgreSQL |
| Content Library | 原始/派生资产、元数据、来源链 | PostgreSQL + S3 |
| Calendar/Analytics | 排期投影、交付和成本聚合 | PostgreSQL read model |

PostgreSQL 保存产品状态；Temporal 保存执行历史。媒体二进制永远只保存在 S3-compatible storage，工作流事件和消息只传引用、哈希和小型元数据。

## 4. 核心领域模型

| 实体 | 关键字段 | 说明 |
| --- | --- | --- |
| Workspace | id、plan、timezone、retention | 租户与计费边界 |
| ConnectorDefinition | key、version、manifest、status | 平台类型和能力 |
| Connection | connector、account、secretRef、scopes、health | 用户授权的具体账号/存储 |
| Subscription | connection、trigger、cursor、lease、expiresAt | webhook/push/poll 状态 |
| Workflow | id、draftVersionId、publishedVersionId、status | 用户可见自动化身份 |
| WorkflowVersion | version、graph、hash、publishedAt | 发布后不可变 |
| Run | workflowVersionId、contentItemId、status、cost | 一次来源事件执行 |
| NodeRun | nodeId、attempt、status、input/output refs | 节点执行与恢复证据 |
| ContentItem | origin、externalId、revision、fingerprint | 统一内容对象 |
| MediaAsset | role、uri、hash、format、duration、dimensions | 原始/派生媒体版本 |
| Approval | policy、steps、actors、deadline、decision | 工作流内治理 |
| Delivery | target、assetVersion、fingerprint、remoteId、status | 每个目标独立交付 |
| DeliveryAttempt | startedAt、response、error、uncertainty | 发布尝试与对账 |

## 5. 工作流节点模型

首版只允许以下节点种类：

- `trigger`: 新内容、云盘文件、RSS 条目、定时、手动。
- `filter`: 媒体类型、时长、关键词、语言、时间、标签。
- `transform`: normalize、resize、crop/pad、trim、subtitle、metadata、backup。
- `composite`: `LocalizeForChina` 等版本化内置动作。
- `approval`: 无审批、单级、条件、多级。
- `destination`: 平台/云盘交付。
- `notification`: 失败、审批、完成通知。

V2.0 约束：单触发器、最大 32 节点、最大 8 个目标、有限条件分支、不允许循环边。循环仅指业务调度的后续触发，不存在图内 while-loop。

## 6. 草稿、编译和发布

### 6.1 草稿编辑

- 草稿可变，保存后生成 revision，但不影响运行中版本。
- 节点参数使用 JSON Schema；敏感值只保存 connection/secret 引用。
- 前端按 connector manifest 动态展示选项与平台限制。

### 6.2 发布前编译

编译器执行：

1. 图结构检查：单入口、可达性、无循环、节点上限。
2. 类型检查：ContentItem/MediaAsset 输入输出兼容。
3. 能力检查：来源、媒体类型、目标账号和动作可用。
4. 连接检查：权限、scope、过期、健康和审核状态。
5. 媒体约束推导：是否存在可生成目标格式的处理链。
6. 风险检查：同平台回流、重复目标、无审批高风险动作。
7. dry run：使用样例内容生成计划和预览，不执行有副作用动作。
8. 生成规范化 graph、hash 和 immutable version。

### 6.3 版本语义

- 每个 Run 固定绑定 `workflow_version_id`。
- 修复草稿后发布新版本，不修改旧版本。
- 失败重放时明确选择“原版本继续”或“使用当前版本从安全节点重跑”。
- 已成功的外部副作用节点默认不重做。

## 7. 执行状态机

Run 建议状态：

```text
queued -> running -> waiting_approval|waiting_schedule|waiting_connection
       -> partially_succeeded -> succeeded
       -> failed_retryable -> running
       -> failed_terminal | cancelled | expired
```

NodeRun 建议状态：

```text
pending -> ready -> running -> succeeded
                         \-> retry_scheduled -> running
                         \-> uncertain -> reconciling -> succeeded|failed
                         \-> failed|cancelled|skipped
```

暂停只阻止调度新的节点；正在向外部平台提交的不可安全中断动作必须完成或进入 `uncertain`，随后回查，不能假装已经取消。

## 8. 一致性、幂等和防环

外部平台无法提供全局 exactly-once。V2 采用：

- 内部至少一次调度。
- 来源去重键：`connector + connection + external_content_id + revision`。
- 内容指纹：原始资产哈希 + 规范化元数据 + origin chain。
- 交付指纹：`workspace + target_connection + asset_version + publish_profile`。
- 发布前 reservation，成功后 remote ID，超时进入 uncertain。
- reconcile 优先回查，再决定是否重试。
- origin chain 保存经过的平台、账号、工作流和派生版本。
- 最大 hop、同平台默认阻断、目标回流检测和人工 override 审计。

## 9. 连接器 SDK

连接器按角色拆接口，不强迫每个平台同时支持来源和目标：

```go
type Connector interface {
    Manifest(ctx context.Context) CapabilityManifest
    ValidateConnection(ctx context.Context, c ConnectionRef) HealthResult
}

type SourceConnector interface {
    Connector
    Subscribe(ctx context.Context, req SubscribeRequest) (SubscriptionState, error)
    Poll(ctx context.Context, req PollRequest) (Page[SourceEvent], error)
    Fetch(ctx context.Context, item SourceItemRef) (FetchedContent, error)
}

type DestinationConnector interface {
    Connector
    ValidateDraft(ctx context.Context, draft DeliveryDraft) ValidationResult
    Publish(ctx context.Context, draft DeliveryDraft, key IdempotencyKey) (PublishResult, error)
    Reconcile(ctx context.Context, ref RemoteOrAttemptRef) (RemoteStatus, error)
    Delete(ctx context.Context, ref RemoteRef) error
}
```

每个连接器包必须包含 manifest、OAuth/scopes、配置 schema、速率策略、fixture 契约测试、错误映射、健康检查和真实凭证验收清单。

## 10. 触发器实现

- webhook/push：签名验证、原始事件落库、快速 2xx、异步规范化和去重。
- poll：每连接独立游标、租约、抖动、退避、配额预算和手动补抓。
- watch renewal：保存 remote channel/resource/expiration，提前续租，失败告警。
- historical import：明确起始时间/数量，单独配额，默认不自动发布所有历史内容。
- manual：用户选择 ContentItem 运行已发布工作流版本。

YouTube 频道通知适合 push，新搜索/分类发现继续 polling；Google Drive watch 会过期，必须有续租任务。[YouTube Push](https://developers.google.com/youtube/v3/guides/push_notifications)，[Drive Push](https://developers.google.com/workspace/drive/api/guides/push)

## 11. 媒体处理实现

### V2.0 处理器

- probe/normalize：容器、编码、帧率、音频、旋转和时长规范化。
- resize：contain/cover、crop/pad、背景模糊、平台安全区预览。
- trim/split：起止时间、固定时间点、多片段输出。
- audio：响度、采样率、声道和静音处理。
- subtitle：保留、提取、生成、翻译、烧录和样式模板。
- branding：封面、片头片尾、固定角标和目标元数据模板。
- backup：原片、派生资产、字幕和 manifest 写入云盘/对象存储。

每个处理器输入/输出按资产 hash 缓存。相同输入和参数不重复转码。长任务用 Activity heartbeat 报告进度和取消检查点。

## 12. V1 适配

V1 适配层包含：

- `LegacyYouTubeMonitorTrigger`：将 V1 monitor candidate 转为 SourceEvent。
- `LocalizeForChinaAction`：创建 V1 task，等待其深度状态机完成，输出视频/字幕/QC 资产。
- `LegacyBilibiliDestination`、`LegacyAcFunDestination`：复用现有发布和对账内核，显式标为兼容模式。

适配层只传引用和状态，不让 V2 直接写 V1 内部表。V1 任务失败时映射成 NodeRun 失败，并保留原 task ID 供诊断。

## 13. 数据库与 API

### 建议表组

```text
automation_workspaces
automation_workflows
automation_workflow_versions
automation_runs
automation_node_runs
connector_definitions
connector_connections
connector_subscriptions
content_items
content_assets
content_origins
approval_requests
approval_steps
delivery_deliveries
delivery_attempts
delivery_remote_snapshots
usage_ledger
audit_events
```

### API 分组

```text
/api/v2/connectors
/api/v2/connections
/api/v2/workflows
/api/v2/workflows/{id}/draft
/api/v2/workflows/{id}/validate
/api/v2/workflows/{id}/dry-run
/api/v2/workflows/{id}/publish
/api/v2/runs
/api/v2/content
/api/v2/approvals
/api/v2/deliveries
/api/v2/calendar
/api/v2/analytics
```

所有写接口带 workspace scope、审计 actor 和乐观并发版本。发布、重放、删除远端内容、连接授权和 kill switch 属于高风险操作，必须有显式确认和审计。

## 14. 安全与合规

- OAuth token/密钥使用 envelope encryption，数据库只保存 secret reference 和必要元数据。
- 每个连接记录 scopes、授权账号、过期时间、最近健康检查和撤销状态。
- webhook 进行签名/nonce/timestamp 验证，设置重放窗口。
- 下载和转发前记录用户权利声明、来源许可和处理依据。
- 任意代码、自定义 shell 和未审核第三方节点不进入 V2.0。
- 媒体下载使用 SSRF 防护、域名/协议策略、大小和时长上限、病毒扫描。
- 审计日志覆盖连接、工作流发布、审批、重放、远端删除和权限变更。
- 数据保留按工作区配置；到期清理 ContentItem 投影、资产和执行数据。

## 15. 可观测性和成本

每个 Run/NodeRun 必须有：trace ID、workflow/version、connector/version、content/asset、attempt、queue latency、duration、bytes、token/minutes、cost、error class。

核心面板：

- 触发延迟、事件去重率、游标落后。
- 节点成功率、P50/P95 时长、重试和取消。
- 连接健康、授权过期、配额和平台 4xx/5xx。
- 交付成功、部分成功、uncertain 和 reconcile 时长。
- 媒体分钟、CPU/GPU、存储、流量和 AI 成本。

## 16. 实施顺序

1. 创建 `/api/v2`、新表和 Connector Manifest，不触碰 V1 状态枚举。
2. 实现 Connection/Secret/Health 与 YouTube、Drive 两个低风险连接器。
3. 完成 Temporal 动态受约束图 PoC 和 Run/NodeRun 投影。
4. 完成 ContentItem/Asset/Origin 和来源事件去重。
5. 完成 dry run、不可变发布版本、暂停/恢复和运行记录。
6. 接入媒体处理器和 `LocalizeForChina`。
7. 接入一个官方发布目标，打通 uncertain/reconcile。
8. 再扩展审批、日历、RBAC、计量和更多连接器。

## 17. 架构退出条件

只有以下证据齐全，架构提案才能从 `proposed` 变为 `accepted`：

- PoC 的 UI/API/持久化/真实执行可演示。
- Worker 重启、服务重启、连接断开、平台超时均可恢复。
- 同一发布动作在 replay 中没有重复投稿。
- 事件历史和成本在目标负载下可接受。
- 安全审计未暴露明文凭证、任意代码和无保护 webhook。
- 团队确认能维护 Temporal Cloud 或自托管部署。
