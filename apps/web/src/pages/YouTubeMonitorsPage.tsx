import { useMemo, useState } from "react";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, ApiError, type YouTubeMonitor } from "../api";
import {
  ConfirmDialog,
  EmptyState,
  LoadingBlock,
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

function monitorDisplayName(item: YouTubeMonitor) {
  const readable = (value?: string) =>
    Boolean(value?.trim()) && !/^[?？\s._-]+$/.test(value?.trim() ?? "");
  if (readable(item.name)) return item.name.trim();
  if (readable(item.series_title)) return item.series_title.trim();
  if (readable(item.query)) return item.query.trim();
  if (item.channel_ids.length > 0) return `频道监控 · ${item.channel_ids[0]}`;
  if (item.monitor_type === "series") {
    return `完整剧集监控（${item.episode_start}–${item.episode_end} 集）`;
  }
  return item.monitor_type === "channel" ? "频道监控" : "关键词监控";
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
  const monitorItems = monitors.data?.items ?? [];
  const histories = useQueries({
    queries: monitorItems.map((item) => ({
      queryKey: ["youtube-monitor-history", item.id],
      queryFn: () => api.youtubeMonitorHistory(item.id),
      staleTime: 15_000,
      refetchInterval: 30_000
    }))
  });
  const latestRunByMonitor = useMemo(
    () => new Map(
      monitorItems.map((item, index) => [item.id, histories[index]?.data?.runs[0]])
    ),
    [histories, monitorItems]
  );

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
      <header className="page-header prototype-monitor-page-header">
        <div className="page-heading">
          <h1>
            YouTube 监控
            <span className="prototype-monitor-count">{stats.total} 个</span>
          </h1>
        </div>
        <div className="page-actions">
          {monitors.data?.items[0] ? (
            <Link className="button button-secondary button-small" to={`/monitors/${monitors.data.items[0].id}/history`}>
              检查记录
            </Link>
          ) : null}
          <Link className="button button-primary" to="/monitors/new">
            新建监控
          </Link>
        </div>
      </header>

      {notice && (
        <TransientNotice
          tone={/失败|错误/.test(notice) ? "error" : "success"}
          onDismiss={() => setNotice("")}
        >
          {notice}
        </TransientNotice>
      )}

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

      <div id="monitor-list" className="monitor-grid prototype-monitor-grid">
        {monitors.data?.items.map((item) => {
          const latestRun = latestRunByMonitor.get(item.id);
          const isError = item.state === "error";
          return (
          <article className={`prototype-monitor-card work-panel ${isError ? "has-error" : ""}`} key={item.id}>
            <div className="prototype-monitor-card-head">
              <span className="prototype-monitor-avatar">
                <Icon name={item.monitor_type === "channel" ? "channel" : item.monitor_type === "series" ? "history" : "discovery"} />
              </span>
              <div className="monitor-copy">
                <h2>{monitorDisplayName(item)}</h2>
                <p>
                  {item.monitor_type === "channel"
                    ? item.channel_ids.join("、") || "未配置频道"
                    : item.monitor_type === "series"
                      ? `${item.series_title} · ${Math.max(1, item.series_scopes.length)} 个篇章 · ${
                          item.series_scopes.length > 0
                            ? item.series_scopes.reduce(
                                (total, scope) => total + scope.episode_end - scope.episode_start + 1,
                                0
                              )
                            : item.episode_end - item.episode_start + 1
                        } 集`
                      : item.query || item.include_keywords.join("、")}
                </p>
              </div>
              <div>
                <span className={`monitor-state state-${item.state}`}>
                  <i aria-hidden="true" />
                  {monitorStateLabel(item)}
                </span>
              </div>
            </div>
            {item.last_error ? (
              <div className="prototype-monitor-error">
                <Icon name="shield" />
                <span><strong>最近一次检查失败</strong> — {friendlyErrorMessage(item.last_error)}</span>
              </div>
            ) : null}
            {!isError ? <dl className="prototype-monitor-metrics">
              <div>
                <dd>{latestRun?.discovered_count ?? 0}</dd>
                <dt>发现新视频</dt>
              </div>
              <div>
                <dd>{latestRun?.task_count ?? 0}</dd>
                <dt>自动建任务</dt>
              </div>
              <div>
                <dd>{item.next_run_at ? formatDateTime(item.next_run_at).slice(5, 16) : "—"}</dd>
                <dt>{item.enabled ? "下次检查" : "已暂停"}</dt>
              </div>
            </dl> : null}
            {!isError ? <div className="prototype-monitor-chips">
              <span>{item.monitor_type === "channel" ? "频道" : item.monitor_type === "series" ? "完整剧集" : "关键词搜索"}</span>
              {item.include_keywords.slice(0, 2).map((keyword) => <span key={keyword}>{keyword}</span>)}
              {(item.min_duration_seconds > 0 || item.max_duration_seconds > 0) ? (
                <span>时长 {Math.round(item.min_duration_seconds / 60)}–{Math.round(item.max_duration_seconds / 60)} 分钟</span>
              ) : null}
              <span>{item.auto_add_to_tasks ? "自动建任务" : "确认后建任务"}</span>
            </div> : null}
            <div className="monitor-row-actions prototype-monitor-actions">
              <button
                className={`button button-small ${isError ? "button-primary" : "button-secondary"}`}
                type="button"
                disabled={stateMutation.isPending || item.state === "running"}
                onClick={() => stateMutation.mutate({ item, action: "run" })}
              >
                {isError ? "重试" : "立即检查"}
              </button>
              <Link className="button button-secondary button-small" to={`/monitors/${item.id}/history`}>
                {isError ? "查看原因" : item.enabled ? "运行记录" : "查看历史"}
              </Link>
              {!isError ? <Link className="button button-secondary button-small" to={`/monitors/${item.id}/edit`}>
                编辑
              </Link> : null}
              <span className="prototype-monitor-action-spacer" />
              <button
                className={item.enabled ? "text-action" : "button button-primary button-small"}
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
              <button
                className="text-action text-danger"
                type="button"
                onClick={() => setDeleteTarget(item)}
              >
                归档
              </button>
            </div>
          </article>
          );
        })}
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
