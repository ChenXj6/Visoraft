import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../api";
import {
  EmptyState,
  LoadingBlock,
  PageHeader,
  QueryError,
  TaskTrack
} from "../components";
import { formatDateTime, formatRelativeTime } from "../format";
import { Icon } from "../icons";

export default function DashboardPage() {
  const dashboard = useQuery({
    queryKey: ["dashboard"],
    queryFn: api.dashboard,
    refetchInterval: 5_000
  });
  const taskList = useQuery({
    queryKey: ["tasks"],
    queryFn: api.tasks,
    refetchInterval: 5_000
  });
  const system = useQuery({
    queryKey: ["system-status"],
    queryFn: api.systemStatus,
    refetchInterval: 8_000
  });
  const cookies = useQuery({
    queryKey: ["cookie-profiles"],
    queryFn: api.cookieProfiles,
    refetchInterval: 15_000
  });

  const failedTasks = taskList.data?.items.filter((task) => task.status === "failed") ?? [];
  const readyCookies =
    cookies.data?.items.filter((profile) => profile.has_usable_cookies).length ?? 0;
  const pipelineCounts = {
    intake:
      taskList.data?.items.filter((task) =>
        ["queued", "fetching_metadata", "metadata_ready"].includes(task.status)
      ).length ?? 0,
    download:
      taskList.data?.items.filter((task) => task.status === "downloading").length ?? 0,
    process:
      taskList.data?.items.filter((task) => task.status === "processing").length ?? 0,
    review:
      taskList.data?.items.filter((task) => task.status === "awaiting_manual_review").length ?? 0,
    ready:
      taskList.data?.items.filter((task) =>
        ["ready_to_publish", "publishing", "published", "reconciled"].includes(task.status)
      ).length ?? 0
  };

  return (
    <>
      <PageHeader
        title="今天的处理队列"
        description="先看异常和待处理项，再进入具体任务。点击任一指标即可查看对应内容。"
      />

      <section
        className={`system-pulse ${system.isError ? "system-pulse-error" : ""}`}
        aria-live="polite"
      >
        <div className="pulse-title">
          <span className="pulse-light" aria-hidden="true" />
          <div>
            <span>系统状态</span>
            <strong>
              {system.isError
                ? "服务暂时不可用"
                : system.isPending
                  ? "正在读取状态"
                  : "运行正常"}
            </strong>
          </div>
        </div>
        <dl>
          <div className="pulse-metric">
            <dt>服务状态</dt>
            <dd>{system.data?.database === "ready" ? "运行正常" : "待检查"}</dd>
            <Link className="metric-hit" to="/settings" aria-label="查看服务设置">查看</Link>
          </div>
          <div className="pulse-metric">
            <dt>待发布任务</dt>
            <dd>{system.data?.pending_outbox ?? "—"}</dd>
            <Link className="metric-hit" to="/tasks?status=active" aria-label="查看待发布任务">查看</Link>
          </div>
          <div className="pulse-metric">
            <dt>最近处理</dt>
            <dd>
              {system.data?.last_worker_event
                ? formatRelativeTime(system.data.last_worker_event)
                : "暂无"}
            </dd>
            <Link className="metric-hit" to="/tasks" aria-label="查看最近任务">查看</Link>
          </div>
          <div className="pulse-metric">
            <dt>登录配置</dt>
            <dd>{cookies.isPending ? "…" : readyCookies}</dd>
            <Link className="metric-hit" to="/cookies" aria-label="查看登录配置">查看</Link>
          </div>
        </dl>
      </section>

      {dashboard.isPending || taskList.isPending ? (
        <LoadingBlock label="正在汇总队列" />
      ) : dashboard.isError ? (
        <QueryError
          title="队列汇总不可用"
          message={dashboard.error.message}
          retry={() => void dashboard.refetch()}
        />
      ) : (
        <section className="pipeline-overview" aria-label="任务处理阶段汇总">
          <header>
            <div>
              <span>实时处理流</span>
              <strong>{dashboard.data.active} 条任务正在流转</strong>
            </div>
            <p>
              <b>{dashboard.data.failed}</b> 条异常 · 共 {dashboard.data.total} 条任务
            </p>
          </header>
          <Link className="pipeline-stage pipeline-stage-active" to="/tasks?status=active" title="查看正在获取视频信息的任务">
            <strong>{pipelineCounts.intake}</strong>
            <span>获取信息</span>
          </Link>
          <Link className="pipeline-stage" to="/tasks?status=active" title="查看正在下载的任务">
            <strong>{pipelineCounts.download}</strong>
            <span>下载媒体</span>
          </Link>
          <Link className="pipeline-stage" to="/tasks?status=active" title="查看正在处理字幕和视频的任务">
            <strong>{pipelineCounts.process}</strong>
            <span>字幕与处理</span>
          </Link>
          <Link className="pipeline-stage" to="/tasks?status=review" title="查看等待人工复核的任务">
            <strong>{pipelineCounts.review}</strong>
            <span>等待复核</span>
          </Link>
          <Link className="pipeline-stage" to="/tasks?status=active" title="查看待发布和发布中的任务">
            <strong>{pipelineCounts.ready}</strong>
            <span>待发布与完成</span>
          </Link>
        </section>
      )}

      <div className="dashboard-workbench">
        <section className="work-panel queue-panel">
          <header className="work-panel-head">
            <div>
              <p className="eyebrow">优先队列</p>
              <h2>{failedTasks.length > 0 ? `${failedTasks.length} 条任务需要处理` : "最近任务"}</h2>
            </div>
            <Link className="text-link" to="/tasks">
              查看全部任务
            </Link>
          </header>

          {taskList.isPending ? (
            <LoadingBlock label="正在读取任务" />
          ) : taskList.isError ? (
            <QueryError message={taskList.error.message} retry={() => void taskList.refetch()} />
          ) : taskList.data.items.length === 0 ? (
            <EmptyState
              title="队列是空的"
              description="创建任务后，信息读取、下载和后续步骤会沿同一条轨道更新。"
              action={
                <Link to="/tasks/new" className="button button-primary">
                  创建第一条任务
                </Link>
              }
            />
          ) : (
            <div className="track-list">
              {taskList.data.items.slice(0, 5).map((task) => (
                <TaskTrack task={task} key={task.id} />
              ))}
            </div>
          )}
        </section>

        <aside className="work-panel operations-panel">
          <header className="work-panel-head">
            <div>
              <p className="eyebrow">运行状态</p>
              <h2>本地链路</h2>
            </div>
          </header>
          <ol className="operations-list">
            <li>
              <span><Icon name="settings" /></span>
              <div>
                <strong>任务服务</strong>
                <p>{system.data ? "运行正常" : "等待连接"}</p>
              </div>
              <i className={system.data ? "op-ready" : "op-wait"}>●</i>
            </li>
            <li>
              <span><Icon name="route" /></span>
              <div>
                <strong>任务投递</strong>
                <p>{system.data ? `${system.data.pending_outbox} 条等待发布` : "状态未知"}</p>
              </div>
              <i className={system.data ? "op-ready" : "op-wait"}>●</i>
            </li>
            <li>
              <span><Icon name="cookie" /></span>
              <div>
                <strong>登录 Cookie</strong>
                <p>{readyCookies > 0 ? `${readyCookies} 个配置可供任务使用` : "尚未配置"}</p>
              </div>
              <Link to="/cookies">{readyCookies > 0 ? "管理" : "配置"}</Link>
            </li>
          </ol>
          <div className="operations-foot">
            <span>最近处理时间</span>
            <strong>{formatDateTime(system.data?.last_worker_event)}</strong>
          </div>
        </aside>
      </div>
    </>
  );
}
