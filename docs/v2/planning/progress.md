# Visoraft V2 规划进度

## 2026-08-14

- 已明确 V1/V2 代际边界，并停止把 V2 描述为 V1 增量改版。
- 已完成 V1 代码底座盘点和第一轮竞品、官方平台 API 研究。
- 已建立文档、流程图、测试证据和计划记录的 V1/V2 目录结构。
- 正在修复迁移后的文档引用与测试输出路径，并建立源码归属清单。
- 正在编写 V2 产品路线图、功能矩阵、实现架构、里程碑和验收标准。
- 已完成 Temporal 与 n8n 商业嵌入边界复核；架构主案收敛为自研控制面、Temporal 执行面和 V1 适配层，待 PoC 决策门验证。
- 已完成 `docs/v1`、`docs/v2`、`diagram/v1`、`diagram/v2`、`artifacts/v1`、`artifacts/v2` 的物理分代和索引。
- 已将根目录 `task_plan.md`、`findings.md`、`progress.md` 改为当前版本入口，V1/V2 详细记录不再混写。
- 已输出 V2 产品路线图、平台能力矩阵、系统架构、目标源码地图、示例契约和验收矩阵。
- 已生成并渲染 V2 工作流执行图和系统架构图，完成第一轮视觉复核与连线修正。
- 已完成最终路径和根入口检查：旧顶层研究/报告目录、旧 `test-artifacts` 均已迁移，V1 有 41 份文档、5 个流程图文件和 269 个本地证据文件；V2 有 11 份规划文档和 4 个流程图/渲染文件，测试证据目录为空是因为尚未实施。
- 49 份 Markdown 的本地链接、2 份 JSON 示例契约和 4 份 SVG XML 均校验通过；`git diff --check` 通过。
- 修改过的 8 个 E2E 脚本均通过 `node --check`；`go test ./...`、`pnpm typecheck`、`pnpm build` 通过，Python 媒体测试 `91 passed, 3 skipped, 3 subtests passed`。

**本次 V2 规划与版本资料整理专项已完成；V2 实现未开始，项目总体继续保持 `in_progress`。**

**状态：`in_progress`；V2 尚未进入功能实现。**
