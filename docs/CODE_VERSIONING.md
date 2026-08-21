# 代码版本策略

## 原则

1. 不复制完整 `apps/`、`internal/`、`workers/` 为 V1/V2 两套源码，避免修复漂移、依赖重复和构建路径破坏。
2. 当前稳定路径默认属于 V1；以 Git tag/分支固定 V1 可恢复基线。
3. V2 新领域使用新模块和 `/api/v2` 契约，不直接改名 V1 表或状态机。
4. 可复用能力通过适配接口接入 V2；V1 原领域仍能独立运行和回归。
5. 每个版本的源码归属、迁移和删除决定都记录在对应 `docs/<version>/code/`。

## 建议分支与标签

| 用途 | 建议 |
| --- | --- |
| V1 已发布基线 | `v0.1.0` 初始封版、`v0.1.1` 交互与本地文件能力刷新版及后续 V1 修复标签 |
| V1 维护 | `release/v1` 或短期修复分支 |
| V2 主开发 | `feature/v2-automation-foundation` 起步，稳定后进入主干 |
| 架构 PoC | 独立短期分支，不混入生产路径 |

## V2 模块边界

V2 实施时优先新增 `internal/automation`、`internal/connectors`、`internal/content`、`internal/deliveries`，以及对应前端页面；现有 `internal/tasks`、`internal/monitors`、`internal/publishing` 通过适配层接入。具体见 [V2 目标源码地图](v2/code/TARGET_SOURCE_MAP.md)。
