# V2 目标源码地图

本文件是实施目标，不代表目录已经存在或功能已经完成。

```text
apps/web/src/
  pages/automations/       工作流列表、创建向导、版本和运行记录
  pages/content/           统一内容库与来源追踪
  pages/calendar/          跨渠道日历
  pages/approvals/         审批队列
  pages/connections/       OAuth/密钥连接与健康状态
  pages/analytics/         交付、失败、成本和效果指标

internal/
  automation/             Workflow、Version、Run、NodeRun、编译器与控制面
  connectors/             SDK、注册表、能力清单、OAuth、订阅与平台实现
  content/                ContentItem、MediaAsset、Origin、指纹和版本
  deliveries/             多目标投递、幂等、回查、部分成功和重放策略
  approvals/              审批策略、步骤、期限和通知
  legacyadapter/           V1 monitor/task/publishing 适配层

cmd/
  automation-worker/      Temporal Worker / 活动执行入口
  connector-scheduler/    webhook 续租、轮询和连接健康检查

workers/media/
  processors/             resize、crop/pad、trim、normalize、字幕、备份
```

## API 与数据边界

- 新 API 使用 `/api/v2`。
- 新表使用 `automation_*`、`connector_*`、`content_*`、`delivery_*` 前缀。
- 数据库迁移建议从独立编号段开始，例如 `000100_automation_foundation.sql`。
- V1 API、状态枚举和表名在兼容期不直接重命名。
