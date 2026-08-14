import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useLocation, useParams } from "react-router-dom";
import { api, ApiError, type YouTubeMonitorHistory } from "../api";
import { LoadingBlock, PageHeader, QueryError } from "../components";
import {
  formatDateTime,
  formatDuration,
  friendlyErrorMessage,
  statusLabel,
  videoTypeLabel
} from "../format";
import { Icon } from "../icons";

function message(error: unknown, fallback: string) {
  return error instanceof ApiError ? error.message : fallback;
}

const decisionLabels: Record<string, string> = {
  accepted: "待建单",
  filtered: "已过滤",
  task_created: "已建单",
  task_failed: "建单失败"
};

type MonitorItem = YouTubeMonitorHistory["items"][number];
type MonitorScope = YouTubeMonitorHistory["monitor"]["series_scopes"][number];

function canEnqueue(item: MonitorItem) {
  return (
    !item.task_id &&
    ["accepted", "duplicate", "task_created", "task_failed"].includes(item.decision)
  );
}

function decisionLabel(item: MonitorItem) {
  if (item.task_id) return "已有任务";
  if (item.decision === "duplicate") return "可加入任务";
  if (item.decision === "task_created") return "可重新加入";
  return decisionLabels[item.decision] ?? "未知结果";
}

function decisionReason(item: MonitorItem) {
  if (item.task_id) return "该视频已经关联任务";
  if (item.decision === "task_created") {
    return "原任务已永久删除，可重新加入任务队列";
  }
  if (
    item.decision === "duplicate" &&
    item.decision_reason.includes("此前已处理")
  ) {
    return "该视频之前已被发现，但尚未关联任务";
  }
  return item.decision_reason;
}

function resolvedScopeKey(item: MonitorItem, scopes: MonitorScope[]) {
  if (item.series_scope_key) return item.series_scope_key;
  if (scopes.length === 1) return scopes[0]?.key ?? "";
  const searchable = `${item.title} ${item.series_scope_name}`.toLocaleUpperCase();
  const matches = scopes
    .map((scope) => ({
      key: scope.key,
      terms: [scope.query, scope.name]
        .map((value) => value.trim().toLocaleUpperCase())
        .filter(Boolean)
    }))
    .filter((scope) => scope.terms.some((term) => searchable.includes(term)))
    .sort(
      (left, right) =>
        Math.max(...right.terms.map((term) => term.length)) -
        Math.max(...left.terms.map((term) => term.length))
    );
  return matches[0]?.key ?? "";
}

export default function YouTubeMonitorHistoryPage() {
  const { monitorId = "" } = useParams();
  const location = useLocation();
  const queryClient = useQueryClient();
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [actionNotice, setActionNotice] = useState("");
  const navigationNotice =
    typeof location.state === "object" &&
    location.state !== null &&
    "notice" in location.state &&
    typeof location.state.notice === "string"
      ? location.state.notice
      : "";
  const history = useQuery({
    queryKey: ["youtube-monitor-history", monitorId],
    queryFn: () => api.youtubeMonitorHistory(monitorId),
    enabled: Boolean(monitorId),
    refetchInterval: (query) => {
      const running = query.state.data?.runs.some((run) =>
        ["queued", "running"].includes(run.status)
      );
      return running ? 2_000 : 10_000;
    }
  });

  const enqueue = useMutation({
    mutationFn: (itemIds: string[]) => api.enqueueYouTubeMonitorItems(monitorId, itemIds),
    onSuccess: async (result) => {
      setSelected(new Set());
      setActionNotice(
        `处理 ${result.requested_count} 条：新建 ${result.created_count} 条，关联已有 ${result.duplicate_count} 条${
          result.failed_count > 0 ? `，失败 ${result.failed_count} 条` : ""
        }。`
      );
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["youtube-monitor-history", monitorId] }),
        queryClient.invalidateQueries({ queryKey: ["tasks"] })
      ]);
    },
    onError: (error) => setActionNotice(message(error, "加入任务失败，请稍后重试"))
  });

  const totals = useMemo(() => {
    const runs = history.data?.runs ?? [];
    return runs.reduce(
      (result, run) => ({
        runs: result.runs + 1,
        discovered: result.discovered + run.discovered_count,
        accepted: result.accepted + run.accepted_count,
        tasks: result.tasks + run.task_count,
        quota: result.quota + run.quota_units
      }),
      { runs: 0, discovered: 0, accepted: 0, tasks: 0, quota: 0 }
    );
  }, [history.data]);

  if (history.isPending) return <LoadingBlock label="正在读取监控运行记录" />;
  if (history.isError || !history.data) {
    return (
      <QueryError
        title="无法读取运行记录"
        message={message(history.error, "监控配置不存在或服务不可用")}
        retry={() => void history.refetch()}
      />
    );
  }

  const monitor = history.data.monitor;
  const displayedItems = history.data.items.filter((item, index, items) =>
    items.findIndex((candidate) => candidate.external_video_id === item.external_video_id) === index
  );
  const eligibleItems = displayedItems.filter(canEnqueue);
  const allEligibleSelected =
    eligibleItems.length > 0 && eligibleItems.every((item) => selected.has(item.id));
  const seriesScopes = monitor.series_scopes.length > 0
    ? monitor.series_scopes
    : [{
        key: "default",
        name: "",
        query: monitor.query,
        episode_start: monitor.episode_start,
        episode_end: monitor.episode_end
      }];
  const seriesCoverage = seriesScopes.map((scope) => {
    const discovered = new Set(
      history.data.items
        .filter(
          (item) =>
            item.episode_number > 0 &&
            item.decision !== "filtered" &&
            resolvedScopeKey(item, seriesScopes) === scope.key
        )
        .map((item) => item.episode_number)
    );
    const expected = Array.from(
      { length: scope.episode_end - scope.episode_start + 1 },
      (_, index) => scope.episode_start + index
    );
    return {
      ...scope,
      discovered,
      expected,
      missing: expected.filter((episode) => !discovered.has(episode))
    };
  });

  const toggleItem = (itemID: string) => {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(itemID)) next.delete(itemID);
      else next.add(itemID);
      return next;
    });
  };

  return (
    <>
      <PageHeader
        title={monitor.name}
        description="发现结果可在这里人工挑选进入任务，也可以在配置中开启自动建单。后续下载、字幕、审核和投稿均走统一任务流水线。"
        actions={
          <div className="page-actions">
            <Link className="button button-secondary" to="/monitors">
              返回列表
            </Link>
            <Link className="button button-primary" to={`/monitors/${monitor.id}/edit`}>
              编辑配置
            </Link>
          </div>
        }
      />

      {(navigationNotice || actionNotice) && (
        <div
          className={`inline-notice ${enqueue.isError ? "inline-notice-error" : ""}`}
          role={enqueue.isError ? "alert" : "status"}
        >
          {actionNotice || navigationNotice}
        </div>
      )}

      <dl className="monitor-ledger monitor-history-ledger">
        <div><dt>运行次数</dt><dd>{totals.runs}</dd></div>
        <div><dt>发现候选</dt><dd>{totals.discovered}</dd></div>
        <div><dt>规则接收</dt><dd>{totals.accepted}</dd></div>
        <div><dt>历史建单</dt><dd>{totals.tasks}</dd></div>
        <div><dt>请求计数</dt><dd>{totals.quota}</dd></div>
      </dl>
      <p className="monitor-history-note">
        以上为运行历史累计；任务被删除后，历史建单记录仍会保留，当前是否存在任务以结果列表为准。
      </p>

      {monitor.monitor_type === "series" && (
        <section className="work-panel monitor-history-panel">
          <header className="section-heading">
            <span className="sequence-mark"><Icon name="history" /></span>
            <div>
              <h2>节目覆盖</h2>
              <p>{monitor.series_title} · {seriesScopes.length} 个篇章分别核对，不再把节目限制为某一部。</p>
            </div>
          </header>
          <div className="series-coverage-grid">
            {seriesCoverage.map((scope, index) => (
              <article key={scope.key}>
                <div>
                  <span>篇章 {index + 1}</span>
                  <strong>{scope.name || "完整节目"}</strong>
                  {scope.query && <small>{scope.query}</small>}
                </div>
                <p>
                  已检索 <strong>{scope.discovered.size}/{scope.expected.length}</strong> 集
                </p>
                <small className={scope.missing.length > 0 ? "coverage-missing" : "coverage-complete"}>
                  {scope.missing.length === 0
                    ? "目标范围已全部检索到"
                    : `尚缺 ${scope.missing.map((episode) => `第 ${episode} 集`).join("、")}`}
                </small>
              </article>
            ))}
          </div>
        </section>
      )}

      <section className="work-panel monitor-history-panel">
        <header className="section-heading">
          <span className="sequence-mark"><Icon name="history" /></span>
          <div>
            <h2>运行批次</h2>
            <p>队列与执行状态来自持久化调度服务，不依赖浏览器页面。</p>
          </div>
        </header>
        {history.data.runs.length === 0 ? (
          <p className="quiet-empty">尚未执行。可返回列表点击“立即执行”。</p>
        ) : (
          <div className="run-list">
            {history.data.runs.map((run) => (
              <article key={run.id}>
                <span className={`run-state run-${run.status}`}>{statusLabel(run.status)}</span>
                <div className="run-summary">
                  <strong>{run.trigger === "scheduled" ? "自动调度" : "手动运行"}</strong>
                  <small>{formatDateTime(run.started_at)}</small>
                  {run.error_message && <p>{friendlyErrorMessage(run.error_message)}</p>}
                </div>
                <dl>
                  <div><dt>发现</dt><dd>{run.discovered_count}</dd></div>
                  <div><dt>接收</dt><dd>{run.accepted_count}</dd></div>
                  <div><dt>重复</dt><dd>{run.duplicate_count}</dd></div>
                  <div><dt>当批建单</dt><dd>{run.task_count}</dd></div>
                </dl>
              </article>
            ))}
          </div>
        )}
      </section>

      <section className="work-panel monitor-history-panel monitor-results-panel">
        <header className="section-heading monitor-results-heading">
          <span className="sequence-mark"><Icon name="discovery" /></span>
          <div>
            <h2>发现结果与后续处理</h2>
            <p>选择待建单结果后加入任务；已存在任务的结果会自动关联，不会重复下载。</p>
          </div>
        </header>
        {history.data.items.length === 0 ? (
          <p className="quiet-empty">当前没有候选记录。</p>
        ) : (
          <>
            <div className="monitor-batch-bar">
              <label className="selection-control">
                <input
                  type="checkbox"
                  checked={allEligibleSelected}
                  disabled={eligibleItems.length === 0}
                  onChange={() =>
                    setSelected(
                      allEligibleSelected
                        ? new Set()
                        : new Set(eligibleItems.map((item) => item.id))
                    )
                  }
                />
                <span>选择全部待建单结果</span>
              </label>
              <div>
                <span>已选 {selected.size} 条</span>
                <button
                  className="button button-primary"
                  type="button"
                  disabled={selected.size === 0 || enqueue.isPending}
                  onClick={() => enqueue.mutate([...selected])}
                >
                  {enqueue.isPending ? "正在加入…" : "加入任务队列"}
                </button>
              </div>
            </div>
            <div className="monitor-result-list" role="list">
              {displayedItems.map((item) => (
                <article className="monitor-result-row" role="listitem" key={item.id}>
                  <div className="monitor-result-select">
                    {canEnqueue(item) ? (
                      <input
                        aria-label={`选择 ${item.title || item.external_video_id}`}
                        type="checkbox"
                        checked={selected.has(item.id)}
                        onChange={() => toggleItem(item.id)}
                      />
                    ) : (
                      <span aria-hidden="true" />
                    )}
                  </div>
                  <div className="monitor-result-video">
                    <a href={item.source_url} target="_blank" rel="noreferrer">
                      {item.title || item.external_video_id}
                    </a>
                    <div className="monitor-result-meta">
                      {item.series_scope_name && <span>{item.series_scope_name}</span>}
                      {item.episode_number > 0 && <span>第 {item.episode_number} 集</span>}
                      <span>{item.external_video_id}</span>
                    </div>
                  </div>
                  <div className="monitor-result-channel">
                    <strong>{item.channel_title || "未知频道"}</strong>
                    <small>{videoTypeLabel(item.video_type)} · {formatDuration(item.duration_seconds)}</small>
                  </div>
                  <div className="monitor-result-metrics">
                    <span>播放 {item.view_count.toLocaleString()}</span>
                    <small>赞 {item.like_count.toLocaleString()} · 评 {item.comment_count.toLocaleString()}</small>
                  </div>
                  <div className="monitor-result-decision">
                    <span className={`decision-tag decision-${item.decision}`}>
                      {decisionLabel(item)}
                    </span>
                    <small>{decisionReason(item)}</small>
                  </div>
                  <div className="monitor-result-action">
                    {item.task_id ? (
                      <Link className="button button-secondary button-small" to={`/tasks/${item.task_id}`}>
                        查看任务
                      </Link>
                    ) : canEnqueue(item) ? (
                      <button
                        className="button button-secondary button-small"
                        type="button"
                        disabled={enqueue.isPending}
                        onClick={() => enqueue.mutate([item.id])}
                      >
                        {item.decision === "task_failed" || item.decision === "task_created"
                          ? "重新加入"
                          : "加入任务"}
                      </button>
                    ) : (
                      <span>无需操作</span>
                    )}
                  </div>
                </article>
              ))}
            </div>
          </>
        )}
      </section>
    </>
  );
}
