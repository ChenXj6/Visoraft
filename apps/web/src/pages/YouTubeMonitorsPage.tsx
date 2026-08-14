import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, ApiError, type YouTubeMonitor } from "../api";
import {
  ConfirmDialog,
  EmptyState,
  LoadingBlock,
  PageHeader,
  QueryError
} from "../components";
import { formatDateTime, friendlyErrorMessage } from "../format";
import { Icon } from "../icons";
import { TransientNotice } from "../product-ui";

function message(error: unknown, fallback: string) {
  return error instanceof ApiError ? error.message : fallback;
}

function monitorStateLabel(item: YouTubeMonitor) {
  if (!item.enabled || item.state === "paused") return "已暂停";
  if (item.state === "running") return "执行中";
  if (item.state === "error") return "异常";
  return item.schedule_type === "automatic" ? "自动监控" : "手动";
}

export default function YouTubeMonitorsPage() {
  const queryClient = useQueryClient();
  const [notice, setNotice] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<YouTubeMonitor>();

  const monitors = useQuery({
    queryKey: ["youtube-monitors"],
    queryFn: api.youtubeMonitors,
    refetchInterval: 5_000
  });

  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ["youtube-monitors"] });
  };

  const stateMutation = useMutation<
    unknown,
    Error,
    { item: YouTubeMonitor; action: "pause" | "resume" | "run" }
  >({
    mutationFn: ({
      item,
      action
    }: {
      item: YouTubeMonitor;
      action: "pause" | "resume" | "run";
    }) => {
      if (action === "pause") return api.pauseYouTubeMonitor(item.id);
      if (action === "resume") return api.resumeYouTubeMonitor(item.id);
      return api.runYouTubeMonitor(item.id);
    },
    onSuccess: async (_result, input) => {
      setNotice(
        input.action === "run"
          ? `“${input.item.name}”已进入执行队列。`
          : input.action === "pause"
            ? `“${input.item.name}”已暂停。`
            : `“${input.item.name}”已恢复。`
      );
      await refresh();
    },
    onError: (error) => setNotice(message(error, "监控操作失败"))
  });

  const remove = useMutation({
    mutationFn: (item: YouTubeMonitor) =>
      api.deleteYouTubeMonitor(item.id, "archive"),
    onSuccess: async () => {
      setNotice("监控配置已归档，运行历史继续保留。");
      setDeleteTarget(undefined);
      await refresh();
    },
    onError: (error) => setNotice(message(error, "监控配置归档失败"))
  });

  const stats = useMemo(() => {
    const items = monitors.data?.items ?? [];
    return {
      total: items.length,
      automatic: items.filter(
        (item) => item.enabled && item.schedule_type === "automatic"
      ).length,
      running: items.filter((item) => item.state === "running").length,
      errors: items.filter((item) => item.state === "error").length
    };
  }, [monitors.data]);

  return (
    <>
      <PageHeader
        title="发现与监控"
        description="按关键词、频道或完整剧集持续发现视频，完成过滤、去重后可进入同一条任务流水线。"
        actions={
          <Link className="button button-primary" to="/monitors/new">
            新建监控
          </Link>
        }
      />

      {notice && (
        <TransientNotice
          tone={/失败|错误/.test(notice) ? "error" : "success"}
          onDismiss={() => setNotice("")}
        >
          {notice}
        </TransientNotice>
      )}

      <dl className="monitor-ledger">
        <div>
          <dt>配置总数</dt>
          <dd>{stats.total}</dd>
        </div>
        <div>
          <dt>自动调度</dt>
          <dd>{stats.automatic}</dd>
        </div>
        <div>
          <dt>执行中</dt>
          <dd>{stats.running}</dd>
        </div>
        <div>
          <dt>异常</dt>
          <dd>{stats.errors}</dd>
        </div>
      </dl>

      {monitors.isPending && <LoadingBlock label="正在读取监控配置" />}
      {monitors.isError && (
        <QueryError
          title="监控配置暂时不可用"
            message={message(monitors.error, "暂时无法连接监控服务")}
          retry={() => void monitors.refetch()}
        />
      )}
      {monitors.data?.items.length === 0 && (
        <EmptyState
          title="还没有监控配置"
          description="创建关键词、频道或完整剧集监控；运行结果会保留过滤判定和建单记录。"
          action={
            <Link className="button button-primary" to="/monitors/new">
              创建第一条监控
            </Link>
          }
        />
      )}

      <div className="monitor-list">
        {monitors.data?.items.map((item) => (
          <article className="monitor-row work-panel" key={item.id}>
            <div className="monitor-mode">
              <Icon name={item.monitor_type === "channel" ? "channel" : item.monitor_type === "series" ? "history" : "discovery"} />
              <strong>
                {item.monitor_type === "channel"
                  ? "频道"
                  : item.monitor_type === "series"
                    ? "剧集"
                    : "搜索"}
              </strong>
            </div>
            <div className="monitor-copy">
              <div>
                <span className={`monitor-state state-${item.state}`}>
                  <i aria-hidden="true" />
                  {monitorStateLabel(item)}
                </span>
                <small>v{item.version}</small>
              </div>
              <h2>{item.name}</h2>
              <p>
                {item.monitor_type === "channel"
                  ? item.channel_ids.join("、") || "未配置频道"
                  : item.monitor_type === "series"
                    ? `${item.series_title} · ${Math.max(1, item.series_scopes.length)} 个篇章 · ${
                        item.series_scopes.length > 0
                          ? item.series_scopes.reduce(
                              (total, scope) =>
                                total + scope.episode_end - scope.episode_start + 1,
                              0
                            )
                          : item.episode_end - item.episode_start + 1
                      } 集`
                  : item.query || item.include_keywords.join("、")}
              </p>
              {item.last_error && (
                <p className="monitor-error">{friendlyErrorMessage(item.last_error)}</p>
              )}
            </div>
            <dl className="monitor-timing">
              <div>
                <dt>调度</dt>
                <dd>
                  {item.schedule_type === "automatic"
                    ? `每 ${item.schedule_interval_minutes} 分钟`
                    : "仅手动"}
                </dd>
              </div>
              <div>
                <dt>最近运行</dt>
                <dd>{item.last_run_at ? formatDateTime(item.last_run_at) : "尚未运行"}</dd>
              </div>
              <div>
                <dt>下次运行</dt>
                <dd>{item.next_run_at ? formatDateTime(item.next_run_at) : "—"}</dd>
              </div>
            </dl>
            <div className="monitor-row-actions">
              <button
                className="button button-primary"
                type="button"
                disabled={stateMutation.isPending || item.state === "running"}
                onClick={() => stateMutation.mutate({ item, action: "run" })}
              >
                立即执行
              </button>
              <button
                className="button button-secondary"
                type="button"
                disabled={stateMutation.isPending}
                onClick={() =>
                  stateMutation.mutate({
                    item,
                    action: item.enabled ? "pause" : "resume"
                  })
                }
              >
                {item.enabled ? "暂停" : "恢复"}
              </button>
              <Link className="button button-secondary" to={`/monitors/${item.id}/history`}>
                运行记录
              </Link>
              <Link className="text-action" to={`/monitors/${item.id}/edit`}>
                编辑
              </Link>
              <button
                className="text-action text-danger"
                type="button"
                onClick={() => setDeleteTarget(item)}
              >
                归档
              </button>
            </div>
          </article>
        ))}
      </div>

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        title="归档监控配置"
        description={`将“${deleteTarget?.name ?? ""}”从配置列表中移除，但保留既有运行与发现历史。`}
        confirmLabel="确认归档"
        busy={remove.isPending}
        destructive
        onClose={() => setDeleteTarget(undefined)}
        onConfirm={() => {
          if (deleteTarget) remove.mutate(deleteTarget);
        }}
      />
    </>
  );
}
