# V1 源码归属地图

当前仓库可运行源码默认归属 V1。目录保持原位以保证 Go module、pnpm workspace、Docker 构建和测试脚本不被破坏。

| 路径 | V1 职责 | V2 处理方式 |
| --- | --- | --- |
| `apps/web` | V1 管理端页面与 API 客户端 | 保留 V1 路由；V2 新增独立导航和页面域 |
| `internal/monitors` | YouTube 搜索/频道监控、调度、候选与自动建单 | 通过 legacy trigger adapter 接入 |
| `internal/tasks` | 固定任务状态、步骤与资产 | 由 `LocalizeForChina` 复合动作调用 |
| `internal/workflow` | V1 固定事件消费者 | 不扩成通用 DAG；保留为 V1 内核 |
| `internal/pipeline` | 固定深度处理阶段推进 | 封装为复合动作，不直接暴露全部内部步骤 |
| `internal/publishing` | Bilibili/AcFun 发布、尝试、回查和不确定结果恢复 | 发布可靠性逻辑可抽取，平台适配器纳入连接器层 |
| `internal/outbox`、`internal/events` | 可靠事件投递 | 保留，并用于 V1/V2 适配边界 |
| `workers/media` | 下载、FFprobe、字幕、ASR、模型与转码 | 逐步改造成 V2 媒体 Activity/处理器 |
| `tests/e2e` | V1 端到端测试 | 保留原位置；输出统一写入 `artifacts/v1/test-runs` |

任何将这些模块标为 V2 原生实现的说法，都必须先有迁移代码、持久化接口、联调和成功/失败/恢复测试证据。
