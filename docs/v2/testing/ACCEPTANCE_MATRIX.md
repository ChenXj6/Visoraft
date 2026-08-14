# Visoraft V2 验收矩阵

> 本矩阵是未来实施的准入标准，不是完成证明。V2 当前状态为 `in_progress`。

## 1. 功能完成定义

每一项功能同时具备以下五类证据，才可标为完成：

1. 可操作前端页面。
2. 持久化后台接口和数据库记录。
3. 真实服务实现，而非固定返回、内存模拟或测试桩。
4. 前后端联调和实际工作流执行。
5. 成功、失败、恢复路径测试。

真实平台无凭证时，只能标“本地契约已验证，待真实凭证验证”。

## 2. Epic 级验收

| Epic | UI | API/持久化 | 服务 | 必测成功/失败/恢复 |
| --- | --- | --- | --- | --- |
| Connection | OAuth/密钥连接、健康、重连 | connection、scope、secretRef、health | 授权刷新/撤销 | 成功连接、拒绝授权、过期重连、scope 减少 |
| Workflow | 向导、草稿、版本、复制、暂停 | workflow/version immutable | compiler + executor | 静态检查失败、发布、编辑不影响运行、恢复旧版本 |
| Trigger | 来源配置、游标/订阅状态 | subscription/event/dedupe | webhook/poll/renew | 重复事件、乱序、过期续租、轮询中断恢复 |
| Content | 内容库、来源、资产版本 | content/asset/origin | fetch/fingerprint | 相同内容去重、资产缺失、hash 不一致恢复 |
| Processing | 参数、预览、进度 | node run + asset refs | FFmpeg/字幕/AI | 成功、格式拒绝、取消、Worker 重启、缓存命中 |
| Approval | 待审、通过、退回、过期 | request/steps/audit | policy/notification | 任一/全部审批、拒绝重提、超时不发、通知重试 |
| Scheduling | 日历、改期、冲突 | schedule/timezone/window | durable timers | 时区/DST、错过窗口、暂停恢复、每日限额 |
| Delivery | 多目标状态和操作 | delivery/attempt/remote snapshot | publish/reconcile/delete | 全成、部分成功、429、超时未知、回查、重复保护 |
| Backup | 目标与保留策略 | backup delivery/asset manifest | Drive/S3 writer | 上传、断点、容量不足、凭证恢复、清理 |
| Governance | 成员、角色、连接权限、审计 | RBAC/audit | authorization | 越权阻断、权限变化、审批者离开、审计导出 |
| Metering | 用量和套餐面板 | usage ledger | aggregation/limits | 幂等计量、配额耗尽、补偿、账单对账 |

## 3. 发布前可靠性用例

- 相同 webhook 投递 10 次，只创建一个来源事件和一个 Run。
- Run 在每个节点前后崩溃，恢复后不丢步骤、不重复外部副作用。
- 多目标中 2 个成功、1 个失败，成功目标不回滚，失败目标可独立重放。
- 发布请求超时后进入 `uncertain`，先回查，确认不存在才重试。
- Connection 失效后工作流进入等待；重连后从安全节点恢复。
- 工作流新版本发布时，旧 Run 保持原版本，新事件使用新版本。
- 用户暂停后不调度新节点；进行中的不可中断发布正确转为完成或 uncertain。
- kill switch 能按工作区、连接器和目标账号阻断新发布。
- 时间窗跨时区和 DST 时不提前、不重复发布。
- 内容经过目标平台再被来源触发时，origin chain 能阻止回流循环。

## 4. 平台连接器验收

每个真实平台必须保存：

- 应用版本、审核状态、scopes、测试账号和区域。
- 官方文档基线日期和 capability manifest 版本。
- 真实上传/发布的远端 ID 和平台状态。
- 认证失败、权限不足、媒体不兼容、限流、5xx、超时、断网恢复。
- 草稿/公开/排期/删除/回查中实际支持的能力。
- 账号撤权与 token 过期后的可恢复路径。

未具备真实平台凭证的 AcFun、TikTok、抖音等连接器不得标为生产闭环。

## 5. UI 与截图规范

- PC 所有可见文字计算后字号不得低于 `12px`。
- 至少验收 1440、1024、768、390、320 宽度；不得横向溢出。
- 浅色/深色、空态、加载、失败、禁用、部分成功、权限不足都要覆盖。
- 截图存放：`artifacts/v2/test-runs/<suite>/<yyyy-mm-dd>/`。
- 文件名：`<viewport>-<page>-<state>-<case>.png`，例如 `desktop-workflow-run-partial-success-001.png`。
- 每个套件同时输出 `report.json`，记录 commit、环境、viewport、最小字号、console/page errors 和用例结果。

## 6. 证据清单模板

```text
Feature:
Version / Commit:
Environment:
UI evidence:
Persistent API evidence:
Real service evidence:
Integration evidence:
Success test:
Failure test:
Recovery test:
External credential status:
Known gaps:
Verdict: incomplete | contract_verified | credential_pending | accepted
```

只有所有必选 Epic 达到 `accepted`，并且外部平台门槛有清晰范围，才可讨论 V2 Beta 或生产就绪；当前不得使用“完整交付”结论。
