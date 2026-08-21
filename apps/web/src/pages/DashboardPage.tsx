import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../api";
import {
  EmptyState,
  LoadingBlock,
  PageHeader,
  PlatformChips,
  QueryError,
  StatusBadge,
  TaskTitle
} from "../components";
import { formatRelativeTime, taskStatusForDisplay } from "../format";
import { Icon } from "../icons";

export default function DashboardPage() {
  const queryClient = useQueryClient();
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
  const retryTask = useMutation({
    mutationFn: api.retryTask,
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
        queryClient.invalidateQueries({ queryKey: ["tasks"] })
      ]);
    }
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
      />

      <section className="dashboard-stats" aria-label="处理队列摘要" aria-live="polite">
        <Link className="dashboard-stat stat-primary" to="/tasks?status=active">
          <span className="dashboard-stat-icon"><Icon name="activity" /></span>
          <span className="dashboard-stat-copy">
            <strong>{dashboard.data?.active ?? "—"}</strong>
            <small>活跃任务</small>
          </span>
        </Link>
        <Link className="dashboard-stat stat-danger" to="/tasks?status=failed">
          <span className="dashboard-stat-icon"><Icon name="errorCircle" /></span>
          <span className="dashboard-stat-copy">
            <strong>{dashboard.data?.failed ?? "—"}</strong>
            <small>失败需处理</small>
          </span>
        </Link>
        <Link className="dashboard-stat stat-warning" to="/reviews">
          <span className="dashboard-stat-icon"><Icon name="review" /></span>
          <span className="dashboard-stat-copy">
            <strong>{pipelineCounts.review}</strong>
            <small>待复核</small>
          </span>
        </Link>
        <Link className="dashboard-stat stat-success" to="/publishing">
          <span className="dashboard-stat-icon"><Icon name="checkCircle" /></span>
          <span className="dashboard-stat-copy">
            <strong>{pipelineCounts.ready}</strong>
            <small>待发布</small>
          </span>
        </Link>
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
              <span>处理进度</span>
              <strong>{dashboard.data.active} 条任务流转中</strong>
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
              <h2>优先队列</h2>
              <p className="panel-subtitle">{failedTasks.length > 0 ? `${failedTasks.length} 条任务需要处理` : "最近任务"}</p>
            </div>
            <Link className="text-link" to="/tasks">
              查看全部
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
              {taskList.data.items.slice(0, 4).map((task) => (
                <article className="prototype-dashboard-task-row" key={task.id}>
                  <span className="prototype-dashboard-task-thumb">
                    {task.thumbnail_url ? <img src={task.thumbnail_url} alt="" loading="lazy" /> : null}
                  </span>
                  <div className="prototype-dashboard-task-main">
                    <div className="prototype-dashboard-task-title">
                      <strong><Link to={`/tasks/${task.id}`}><TaskTitle task={task} /></Link></strong>
                      <StatusBadge status={taskStatusForDisplay(task)} />
                    </div>
                    <div className="prototype-dashboard-task-meta">
                      <PlatformChips platforms={task.target_platforms} />
                      <span>·</span>
                      <span>{task.error_message || (task.paused_at ? "任务已暂停" : formatRelativeTime(task.updated_at))}</span>
                    </div>
                  </div>
                  <div className="prototype-dashboard-task-action">
                    {
                    task.status === "failed" ? (
                      <button
                        className="button button-secondary button-small"
                        type="button"
                        disabled={retryTask.isPending && retryTask.variables === task.id}
                        onClick={() => retryTask.mutate(task.id)}
                      >
                        {retryTask.isPending && retryTask.variables === task.id ? "重试中" : "重试"}
                      </button>
                    ) : task.status === "awaiting_manual_review" ? (
                      <Link className="button button-text button-small" to={`/reviews/${task.id}`}>审核</Link>
                    ) : ["published", "reconciled"].includes(task.status) ? (
                      <Link className="button button-text button-small" to={`/tasks/${task.id}`}>查看</Link>
                    ) : (
                      <Link className="button button-text button-small" to={`/tasks/${task.id}`}>详情</Link>
                    )
                  }
                  </div>
                </article>
              ))}
            </div>
          )}
        </section>

        <aside className="work-panel operations-panel">
          <header className="work-panel-head">
            <div>
              <h2>运行状态</h2>
            </div>
          </header>
          <ol className="operations-list">
            <li>
              <span><Icon name="settings" /></span>
              <div>
                <strong>任务服务</strong>
                <p>{system.data ? "运行正常" : "等待连接"}</p>
              </div>
              <i className={system.data ? "op-status op-success" : "op-status op-wait"}>{system.data ? "正常" : "等待"}</i>
            </li>
            <li>
              <span><Icon name="route" /></span>
              <div>
                <strong>任务投递</strong>
                <p>{system.data ? `${system.data.pending_outbox} 条等待发布` : "状态未知"}</p>
              </div>
              <i className="op-status op-count">{system.data?.pending_outbox ?? 0}</i>
            </li>
            <li>
              <span><Icon name="cookie" /></span>
              <div>
                <strong>登录 Cookie</strong>
                <p>{readyCookies > 0 ? `${readyCookies} 个配置可供任务使用` : "尚未配置"}</p>
              </div>
              <Link to="/cookies">{readyCookies > 0 ? "管理" : "配置"}</Link>
            </li>
            <li>
              <span><Icon name="history" /></span>
              <div>
                <strong>最近处理</strong>
                <p>{system.data?.last_worker_event ? formatRelativeTime(system.data.last_worker_event) : "暂无记录"}</p>
              </div>
            </li>
          </ol>
        </aside>
      </div>
    </>
  );
}
