# V2 平台能力与上线分期

> 状态：规划。平台能力、审核政策和限额会变化；每次进入开发前必须以官方文档和应用后台复核。

## 优先级原则

1. 官方 OAuth/API 优先于 Cookie 和非公开接口。
2. 先覆盖完整用户路径，再追求平台 Logo 数量。
3. 来源、目标、备份、媒体类型和回查能力分别标注。
4. 未拿到开发者主体、应用审核或真实账号凭证时，只能完成本地契约测试，不能标生产闭环。

## 分期矩阵

| 连接器 | 来源 | 目标 | 备份 | 阶段 | 关键依赖/限制 |
| --- | --- | --- | --- | --- | --- |
| 本地上传 | 是 | 否 | 否 | P0 / V2.0 | 权利确认、断点上传、病毒扫描 |
| S3-compatible | 文件/对象事件 | 上传 | 是 | P0 / V2.0 | webhook 或安全轮询、生命周期、流量成本 |
| Google Drive | 文件/changes | 上传 | 是 | P0 / V2.0 | OAuth、watch channel 续租、共享盘权限 |
| YouTube | 频道推送、搜索轮询 | OAuth 上传 | 可选 | P0 / V2.0 | 配额、应用审核、隐私与受众字段 |
| 视频/音频 RSS | 新条目轮询 | 否 | 可转存 | P0 / V2.0 | ETag/Last-Modified、源站下载权限 |
| Bilibili Open Platform | 待能力确认 | 投稿/删除/查询 | 否 | P0 / V2.0 | 开放平台应用身份与 OAuth 审核 |
| Bilibili Legacy | 否 | Cookie 投稿 | 否 | 兼容 | 高维护风险，必须可单独关闭 |
| V1 LocalizeForChina | 复合处理 | 非平台 | 派生资产 | P0 / V2.0 | 适配 V1 task/pipeline/publishing |
| Dropbox | 文件变更 | 上传 | 是 | P1 / V2.1 | OAuth、webhook、下载/上传限额 |
| 抖音 | 授权内容候选 | 视频/图片发布 | 否 | P1 / V2.1 | 主体、应用审核、授权、平台水印政策 |
| TikTok | 授权元数据 | Direct Post | 否 | P1 / V2.1 | Display API 无原始下载；公开发布需审核 |
| AcFun Legacy | 否 | 投稿 | 否 | P1 / 待凭证 | 当前仍待真实账号成功/失败/恢复验收 |
| Instagram/Facebook | 待复核 | 待复核 | 否 | P1 候选 | 官方文档、权限与 App Review 待当前复核 |
| 头条/西瓜 | 授权内容候选 | 发布 | 否 | P1/P2 | 复用字节 OAuth/上传框架，规格独立 |
| X/LinkedIn/Threads/Pinterest | 视官方能力 | 发布 | 否 | P2 | 成本、审核、媒体规格与商业优先级 |
| 小红书/视频号 | 不承诺 | 不承诺 | 否 | 观察 | 只有在官方商业接口明确且获批后进入计划 |

## 连接器能力清单字段

每个连接器版本必须声明：

- `roles`: source、destination、backup。
- `mediaTypes`: video、shortVideo、image、carousel、audio、liveReplay。
- `accountTypes` 和所需 scopes。
- trigger：webhook、push、poll、manual；游标与订阅到期机制。
- action：fetch、upload、publish、draft、schedule、comment、delete、reconcile。
- 媒体限制：容器、编码、大小、时长、宽高比、帧率、音频。
- 文本限制：标题、正文、标签、分类和受众字段。
- 配额/速率、并发、重试建议和幂等能力。
- 连接健康、权限过期、平台审核、套餐和地区限制。

工作流编辑器只能展示当前连接和目标账号真实可用的组合。
