import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import {
  api,
  ApiError,
  type Task,
  type TaskArchivePreview
} from "../api";
import {
  EmptyState,
  LoadingBlock,
  ModalDialog,
  PageHeader,
  PlatformChips,
  ProgressBar,
  QueryError,
  StatusBadge,
  TaskTitle,
  currentStep
} from "../components";
import {
  formatRelativeTime,
  formatDateTime,
  orderedTaskSteps,
  shortID,
  stepLabel,
  taskStatusForDisplay,
  taskStepProgress
} from "../format";
import { Icon } from "../icons";
import {
  TaskLifecycleDialog,
  type TaskLifecycleMode
} from "./TaskLifecycleDialog";
import { TransientNotice } from "../product-ui";

type Filter = "all" | "active" | "review" | "failed" | "done";
type Scope = "active" | "archived";

const filters: { key: Filter; label: string }[] = [
  { key: "all", label: "全部" },
  { key: "active", label: "流转中" },
  { key: "review", label: "待复核" },
  { key: "failed", label: "失败" },
  { key: "done", label: "已完成" }
];

const archivableStatuses = new Set([
  "awaiting_manual_review",
  "ready_to_publish",
  "published",
  "reconciled",
  "failed",
  "cancelled",
  "abandoned"
]);

const pausableStatuses = new Set([
  "queued",
  "fetching_metadata",
  "metadata_ready",
  "downloading",
  "processing",
  "awaiting_manual_review",
  "ready_to_publish"
]);

function isFilter(value: string | null): value is Filter {
  return filters.some((filter) => filter.key === value);
}

function matchesFilter(status: string, filter: Filter) {
  if (filter === "all") return true;
  if (filter === "review") return status === "awaiting_manual_review";
  if (filter === "failed") return status === "failed";
  if (filter === "done") return ["published", "reconciled"].includes(status);
  return [
    "queued",
    "fetching_metadata",
    "metadata_ready",
    "downloading",
    "processing",
    "ready_to_publish",
    "publishing"
  ].includes(status);
}

function messageOf(error: unknown, fallback: string) {
  return error instanceof ApiError ? error.message : fallback;
}

type DialogState = {
  mode: TaskLifecycleMode;
  task?: Task;
  preview?: TaskArchivePreview;
};

export default function TasksPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const scope: Scope = searchParams.get("scope") === "archived" ? "archived" : "active";
  const rawFilter = searchParams.get("status");
  const filter: Filter = isFilter(rawFilter) ? rawFilter : "all";
  const [selected, setSelected] = useState<string[]>([]);
  const [notice, setNotice] = useState("");
  const [actionError, setActionError] = useState("");
  const [search, setSearch] = useState("");
  const [dialog, setDialog] = useState<DialogState | null>(null);
  const [previewTask, setPreviewTask] = useState<Task>();
  const queryClient = useQueryClient();

  useEffect(() => {
    setSelected([]);
    setNotice("");
    setActionError("");
  }, [scope]);

  const tasks = useQuery({
    queryKey: ["tasks", scope],
    queryFn: () => api.tasksByScope(scope),
    refetchInterval: 4_000
  });
  const archivePreview = useQuery({
    queryKey: ["task-archive-preview"],
    queryFn: api.taskArchivePreview,
    enabled: scope === "active"
  });

  const refreshTaskViews = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["tasks"] }),
      queryClient.invalidateQueries({ queryKey: ["task-archive-preview"] }),
      queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
      queryClient.invalidateQueries({ queryKey: ["reviews"] }),
      queryClient.invalidateQueries({ queryKey: ["publishing-queue"] })
    ]);
  };

  const bulkRetry = useMutation({
    mutationFn: api.retryTasks,
    onSuccess: async (result) => {
      const succeeded = new Set(result.succeeded.map((task) => task.id));
      setSelected((current) => current.filter((taskID) => !succeeded.has(taskID)));
      setNotice(
        result.failed.length === 0
          ? `已重新投递 ${result.succeeded.length} 条任务`
          : `已投递 ${result.succeeded.length} 条，${result.failed.length} 条未执行`
      );
      await refreshTaskViews();
    },
    onError: (error) => setNotice(messageOf(error, "批量重试失败"))
  });

  const taskControl = useMutation({
    mutationFn: ({ task, action }: { task: Task; action: "pause" | "resume" }) =>
      action === "pause" ? api.pauseTask(task.id) : api.resumeTask(task.id),
    onSuccess: async (updated) => {
      setActionError("");
      setNotice(updated.paused_at ? "任务已暂停，可随时继续处理" : "任务已继续处理");
      await refreshTaskViews();
    },
    onError: (error) => setActionError(messageOf(error, "任务状态未能更新"))
  });

  const archiveTask = useMutation({
    mutationFn: ({
      task,
      deleteAssets,
      reason
    }: {
      task: Task;
      deleteAssets: boolean;
      reason: string;
    }) =>
      api.archiveTask(task.id, {
        expected_version: task.version,
        delete_assets: deleteAssets,
        reason
      }),
    onSuccess: async (_, input) => {
      setDialog(null);
      setActionError("");
      setNotice(
        input.deleteAssets
          ? "任务已移入回收站，媒体文件正在后台清理"
          : "任务已移入回收站，媒体文件已保留"
      );
      await refreshTaskViews();
    },
    onError: (error) => setActionError(messageOf(error, "任务未能移入回收站"))
  });

  const archiveAll = useMutation({
    mutationFn: ({
      preview,
      deleteAssets,
      reason
    }: {
      preview: TaskArchivePreview;
      deleteAssets: boolean;
      reason: string;
    }) =>
      api.archiveAllTasks({
        expected_count: preview.total_tasks,
        delete_assets: deleteAssets,
        confirmation: `archive-all:${preview.total_tasks}`,
        reason
      }),
    onSuccess: async (result) => {
      setDialog(null);
      setActionError("");
      setNotice(
        result.failed.length === 0
          ? `已将 ${result.archived.length} 条任务移入回收站`
          : `已移入 ${result.archived.length} 条；${result.failed.length} 条运行中任务已保留`
      );
      await refreshTaskViews();
    },
    onError: (error) => setActionError(messageOf(error, "清空任务列表失败"))
  });

  const restoreTask = useMutation({
    mutationFn: ({ task, reason }: { task: Task; reason: string }) =>
      api.restoreTask(task.id, {
        expected_version: task.version,
        reason
      }),
    onSuccess: async () => {
      setDialog(null);
      setActionError("");
      setNotice("任务已恢复到工作列表");
      await refreshTaskViews();
    },
    onError: (error) => setActionError(messageOf(error, "任务恢复失败"))
  });

  const purgeTask = useMutation({
    mutationFn: ({ task, reason }: { task: Task; reason: string }) =>
      api.purgeTask(task.id, {
        expected_version: task.version,
        confirmation: `purge:${task.id}`,
        reason
      }),
    onSuccess: async () => {
      setDialog(null);
      setActionError("");
      setNotice("任务记录已永久删除；远端平台稿件未做任何操作");
      await refreshTaskViews();
    },
    onError: (error) => setActionError(messageOf(error, "任务记录永久删除失败"))
  });

  const cleanupAssets = useMutation({
    mutationFn: api.deleteTaskAssets,
    onSuccess: async () => {
      setNotice("媒体文件已进入后台清理队列");
      await refreshTaskViews();
    },
    onError: (error) => setNotice(messageOf(error, "媒体文件清理失败"))
  });

  const visibleTasks = useMemo(
    () => {
      const normalized = search.trim().toLocaleLowerCase();
      const scoped = scope === "archived"
        ? tasks.data?.items ?? []
        : tasks.data?.items.filter((task) => matchesFilter(task.status, filter)) ?? [];
      if (!normalized) return scoped;
      return scoped.filter((task) =>
        `${task.title} ${task.original_title} ${task.source_url} ${task.category}`
          .toLocaleLowerCase()
          .includes(normalized)
      );
    },
    [filter, scope, search, tasks.data]
  );
  const counts = useMemo(
    () =>
      Object.fromEntries(
        filters.map((item) => [
          item.key,
          tasks.data?.items.filter((task) => matchesFilter(task.status, item.key)).length ?? 0
        ])
      ) as Record<Filter, number>,
    [tasks.data]
  );
  const selectedFailed = useMemo(
    () => visibleTasks.filter((task) => selected.includes(task.id) && task.status === "failed").map((task) => task.id),
    [selected, visibleTasks]
  );
  const selectedVisibleCount = visibleTasks.filter((task) => selected.includes(task.id)).length;
  const allVisibleSelected = visibleTasks.length > 0 && selectedVisibleCount === visibleTasks.length;

  const dialogBusy =
    archiveTask.isPending ||
    archiveAll.isPending ||
    restoreTask.isPending ||
    purgeTask.isPending;

  const submitDialog = ({
    deleteAssets,
    reason
  }: {
    deleteAssets: boolean;
    reason: string;
  }) => {
    if (!dialog) return;
    setActionError("");
    if (dialog.mode === "archive" && dialog.task) {
      archiveTask.mutate({ task: dialog.task, deleteAssets, reason });
    } else if (dialog.mode === "archive_all" && dialog.preview) {
      archiveAll.mutate({ preview: dialog.preview, deleteAssets, reason });
    } else if (dialog.mode === "restore" && dialog.task) {
      restoreTask.mutate({ task: dialog.task, reason });
    } else if (dialog.mode === "purge" && dialog.task) {
      purgeTask.mutate({ task: dialog.task, reason });
    }
  };

  return (
    <>
      <PageHeader
        title={scope === "archived" ? "任务回收站" : "媒体任务"}
        actions={
          <>
            <button
              type="button"
              className="button button-secondary button-small"
              onClick={() => setSearchParams(scope === "active" ? { scope: "archived" } : {})}
            >
              {scope === "active" ? "回收站" : "返回工作列表"}
            </button>
            <Link to="/tasks/new" className="button button-primary">
              <Icon name="plus" />
              新建任务
            </Link>
          </>
        }
      />

      <section className="task-control-strip prototype-task-toolbar" aria-label="任务筛选与批量操作">
        {scope === "active" ? (
          <div className="filter-tabs" role="tablist" aria-label="任务状态筛选">
            {filters.map((item) => (
              <button
                type="button"
                role="tab"
                aria-selected={filter === item.key}
                className={filter === item.key ? "active" : ""}
                onClick={() =>
                  setSearchParams(
                    item.key === "all" ? {} : { status: item.key },
                    { replace: true }
                  )
                }
                key={item.key}
              >
                <span>{item.label}</span>
                <small>{counts[item.key]}</small>
              </button>
            ))}
          </div>
        ) : <div className="prototype-task-toolbar-spacer" />}
        <label className="task-table-search">
          <span className="sr-only">搜索任务</span>
          <Icon name="search" />
          <input
            type="search"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="搜索任务"
          />
        </label>
        {scope === "active" ? (
          <div className="task-control-actions">
            <button
              className="button button-secondary"
              type="button"
              disabled={selectedFailed.length === 0 || bulkRetry.isPending}
              onClick={() => bulkRetry.mutate(selectedFailed)}
            >
              {bulkRetry.isPending
                ? "正在投递…"
                : selectedFailed.length > 0
                  ? `重试失败项 ${selectedFailed.length}`
                  : "选择失败项后重试"}
            </button>
            <button
              className="button button-secondary"
              type="button"
              disabled={
                archivePreview.isPending ||
                !archivePreview.data ||
                archivePreview.data.total_tasks === 0
              }
              onClick={() => {
                if (archivePreview.data) {
                  setActionError("");
                  setDialog({ mode: "archive_all", preview: archivePreview.data });
                }
              }}
            >
              清空任务
            </button>
            <button
              className="icon-button"
              type="button"
              aria-label="刷新任务"
              onClick={() => void tasks.refetch()}
            >
              ↻
            </button>
          </div>
        ) : null}
      </section>

      {notice ? (
        <TransientNotice
          tone={/失败|错误/.test(notice) ? "error" : "success"}
          onDismiss={() => setNotice("")}
        >
          {notice}
        </TransientNotice>
      ) : null}

      {actionError && !dialog ? (
        <TransientNotice tone="error" onDismiss={() => setActionError("")}>
          {actionError}
        </TransientNotice>
      ) : null}

      {selected.length > 0 ? (
        <div className="task-batch-bar" role="status">
          <span className="task-batch-icon" aria-hidden="true"><Icon name="review" /></span>
          <strong>已选择 {selected.length} 个任务{selectedFailed.length > 0 ? `，其中 ${selectedFailed.length} 个可重试` : ""}</strong>
          {selectedFailed.length > 0 ? <button className="button button-primary" type="button" disabled={bulkRetry.isPending} onClick={() => bulkRetry.mutate(selectedFailed)}>
            {bulkRetry.isPending ? "正在重试…" : "重试"}
          </button> : null}
          <button className="button button-secondary" type="button" onClick={() => setSelected([])}>取消选择</button>
        </div>
      ) : null}

      {tasks.isPending ? (
        <LoadingBlock label="正在加载任务列表" />
      ) : tasks.isError ? (
        <QueryError message={tasks.error.message} retry={() => void tasks.refetch()} />
      ) : visibleTasks.length === 0 ? (
        <EmptyState
          title={
            scope === "archived"
              ? "回收站为空"
              : tasks.data.items.length === 0
                ? "还没有任务"
                : "这个筛选下没有任务"
          }
          description={
            scope === "archived"
              ? "移入回收站的任务会出现在这里，防止误删后无法恢复。"
              : tasks.data.items.length === 0
                ? "输入一个视频 URL，任务会进入持久队列并沿处理轨道更新。"
                : "切换状态筛选查看其他任务。"
          }
          action={
            scope === "active" && tasks.data.items.length === 0 ? (
              <Link to="/tasks/new" className="button button-primary">
                新建任务
              </Link>
            ) : scope === "active" ? (
              <button
                className="button button-secondary"
                type="button"
                onClick={() => {
                  setSearch("");
                  setSearchParams({}, { replace: true });
                }}
              >
                清除筛选
              </button>
            ) : undefined
          }
        />
      ) : (
        <section className="work-panel task-list-panel prototype-task-table" aria-label="任务列表">
          <div className="prototype-task-table-head">
            <label className="prototype-task-check prototype-task-check-all">
              <input
                type="checkbox"
                checked={allVisibleSelected}
                aria-label="选择当前列表全部任务"
                onChange={(event) => {
                  const visibleIDs = new Set(visibleTasks.map((task) => task.id));
                  setSelected((current) => event.target.checked
                    ? [...new Set([...current, ...visibleIDs])]
                    : current.filter((taskID) => !visibleIDs.has(taskID)));
                }}
              />
            </label>
            <span>任务</span>
            <span>平台</span>
            <span>状态</span>
            <span>进度</span>
            <span>操作</span>
          </div>
          <div className="prototype-task-table-body">
            {visibleTasks.map((task) => {
              const step = currentStep(task);
              const progress = step ? taskStepProgress(step) : 0;
              const remainingAssets = task.assets.filter(
                (asset) => asset.status !== "deleted"
              );
              const cleanupPending = task.assets.some(
                (asset) => asset.status === "deleting"
              );
              const cleanupAvailable = task.assets.some((asset) =>
                ["available", "failed"].includes(asset.status)
              );
              return (
                <article className="prototype-task-row" key={task.id}>
                  <label className="prototype-task-check">
                    <input
                      type="checkbox"
                      checked={selected.includes(task.id)}
                      aria-label={`选择任务 ${shortID(task.id)}`}
                      onChange={(event) =>
                        setSelected((current) =>
                          event.target.checked
                            ? [...new Set([...current, task.id])]
                            : current.filter((taskID) => taskID !== task.id)
                        )
                      }
                    />
                  </label>
                  <div className="prototype-task-identity">
                    {task.thumbnail_url ? (
                      <img src={task.thumbnail_url} alt="" loading="lazy" />
                    ) : (
                      <span className="prototype-task-thumb"><Icon name="media" /></span>
                    )}
                    <div>
                      <strong><Link to={`/tasks/${task.id}`}><TaskTitle task={task} /></Link></strong>
                      <small title={task.source_url}>
                        {task.source_url.replace(/^https?:\/\//, "").slice(0, 42)} · {formatRelativeTime(task.updated_at)}
                      </small>
                      {task.error_message ? <em>{task.error_message}</em> : null}
                    </div>
                  </div>
                  <PlatformChips platforms={task.target_platforms} />
                  <StatusBadge status={taskStatusForDisplay(task)} />
                  <div className="prototype-task-progress">
                    <ProgressBar
                      value={progress}
                      label={`${step ? stepLabel(step.kind) : "任务"}进度`}
                      tone={step?.status === "failed" ? "danger" : task.paused_at ? "paused" : "primary"}
                      compact
                    />
                    <small>{task.paused_at ? "已暂停" : step ? `${stepLabel(step.kind)} ${progress.toFixed(0)}%` : "等待处理"}</small>
                  </div>
                  <div className="prototype-task-actions">
                    {scope === "active" ? (
                      <>
                        <button className="button button-secondary" type="button" onClick={() => setPreviewTask(task)}>
                          详情
                        </button>
                        {(Boolean(task.paused_at) || pausableStatuses.has(task.status)) && (
                          <button
                            className="button button-text"
                            type="button"
                            disabled={taskControl.isPending}
                            onClick={() =>
                              taskControl.mutate({
                                task,
                                action: task.paused_at ? "resume" : "pause"
                              })
                            }
                          >
                            {task.paused_at ? "继续" : "暂停"}
                          </button>
                        )}
                      </>
                    ) : (
                      <>
                        <button
                          className="button button-secondary"
                          type="button"
                          disabled={cleanupPending}
                          onClick={() => {
                            setActionError("");
                            setDialog({ mode: "restore", task });
                          }}
                        >
                          恢复
                        </button>
                        {cleanupAvailable ? (
                          <button
                            className="button button-secondary"
                            type="button"
                            disabled={cleanupAssets.isPending}
                            onClick={() => cleanupAssets.mutate(task.id)}
                          >
                            清理
                          </button>
                        ) : cleanupPending ? (
                          <button className="button button-secondary" type="button" disabled>
                            清理中
                          </button>
                        ) : null}
                        <button
                          className="button button-danger"
                          type="button"
                          disabled={remainingAssets.length > 0}
                          title={
                            remainingAssets.length > 0
                              ? "先清理全部媒体文件"
                              : "永久删除任务记录"
                          }
                          onClick={() => {
                            setActionError("");
                            setDialog({ mode: "purge", task });
                          }}
                        >
                          永久删除
                        </button>
                      </>
                    )}
                  </div>
                </article>
              );
            })}
          </div>
        </section>
      )}

      {dialog ? (
        <TaskLifecycleDialog
          open
          mode={dialog.mode}
          task={dialog.task}
          preview={dialog.preview}
          busy={dialogBusy}
          error={actionError}
          onClose={() => {
            if (!dialogBusy) {
              setDialog(null);
              setActionError("");
            }
          }}
          onConfirm={submitDialog}
        />
      ) : null}

      <ModalDialog
        open={Boolean(previewTask)}
        title={previewTask ? `任务详情 · ${previewTask.title || previewTask.original_title || shortID(previewTask.id)}` : "任务详情"}
        description={previewTask ? `任务 ${shortID(previewTask.id)} · 创建于 ${formatDateTime(previewTask.created_at)} · ${previewTask.extractor || "视频来源"}` : undefined}
        icon="file"
        size="wide"
        onClose={() => setPreviewTask(undefined)}
        footer={previewTask ? (
          <>
            <button
              className="button button-secondary"
              type="button"
              onClick={() => {
                void navigator.clipboard.writeText(previewTask.id).then(() => setNotice("任务 ID 已复制"));
              }}
            >
              复制 ID
            </button>
            {archivableStatuses.has(previewTask.status) ? (
              <button
                className="button button-text button-text-danger"
                type="button"
                onClick={() => {
                  setPreviewTask(undefined);
                  setActionError("");
                  setDialog({ mode: "archive", task: previewTask });
                }}
              >
                移入回收站
              </button>
            ) : null}
            <span className="modal-footer-spacer" />
            <Link className="button button-primary" to={`/tasks/${previewTask.id}`} onClick={() => setPreviewTask(undefined)}>
              打开完整详情
            </Link>
          </>
        ) : undefined}
      >
        {previewTask ? (
          <div className="task-quick-dialog">
            <div className="task-quick-stages">
              {orderedTaskSteps(previewTask.steps).slice(0, 5).map((step) => (
                <div key={step.kind}>
                  <small>{stepLabel(step.kind)}</small>
                  <strong>{["succeeded", "skipped"].includes(step.status) ? "完成" : step.status === "running" ? `处理中 ${taskStepProgress(step).toFixed(0)}%` : step.status === "failed" ? "失败" : "待启动"}</strong>
                </div>
              ))}
            </div>
            <div className="task-quick-columns">
              <section>
                <h3>输入信息</h3>
                <dl>
                  <div><dt>视频源</dt><dd title={previewTask.source_url}>{previewTask.source_url}</dd></div>
                  <div><dt>投稿策略</dt><dd>{previewTask.posting_strategy_id ? "已选择投稿策略" : "默认策略"}</dd></div>
                  <div><dt>目标平台</dt><dd>{previewTask.target_platforms.join("、") || "未选择"}</dd></div>
                  <div><dt>审核方式</dt><dd>{previewTask.review_mode === "automatic" ? "自动审核" : "人工审核"}</dd></div>
                </dl>
              </section>
              <section>
                <h3>最近处理</h3>
                <div className="task-quick-log">
                  {orderedTaskSteps(previewTask.steps).slice(-5).map((step) => (
                    <p key={step.kind}>
                      <span>{formatDateTime(step.updated_at)}</span>
                      <strong>{stepLabel(step.kind)}</strong>
                      <em>{["succeeded", "skipped"].includes(step.status) ? "完成" : step.status === "failed" ? "失败" : step.status === "running" ? "处理中" : "等待"}</em>
                    </p>
                  ))}
                </div>
              </section>
            </div>
          </div>
        ) : null}
      </ModalDialog>
    </>
  );
}
