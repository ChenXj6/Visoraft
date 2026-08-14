# V1 测试证据索引

## 公开验收入口

- [V1 当前状态与缺项](../planning/CURRENT_STATUS.md)
- [`diagram/v1/full-test-flow`](../../../diagram/v1/full-test-flow/)
- [`diagram/v1/real-e2e-test-flow`](../../../diagram/v1/real-e2e-test-flow/)

## 本地证据目录

| 目录 | 内容 |
| --- | --- |
| `artifacts/v1/acceptance` | 功能验收、真实发布、审核分支证据 |
| `artifacts/v1/test-runs` | Playwright、响应式、暗色模式和操作回归输出 |
| `artifacts/v1/ui-audit` | UI 和发布恢复截图 |
| `artifacts/v1/monitor-*` | 监控易用性与去重专项 |
| `artifacts/v1/subtitle-stall` | 字幕停滞诊断与恢复证据 |
| `artifacts/v1/research` | 原产品黑盒截图 |
| `artifacts/v1/backups` | 测试前数据备份；含敏感本地数据，不入库 |

证据目录、按日期测试报告和详细实施日志默认由 `.gitignore` 排除。它们包含真实任务标识、平台回执或运行环境信息，只保留在本地。对外提供前必须检查账号、Cookie、远端稿件、个人信息和数据库内容。
