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
  PageHeader,
  QueryError,
  TaskTrack
} from "../components";
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
  const [dialog, setDialog] = useState<DialogState | null>(null);
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
    () =>
      scope === "archived"
        ? tasks.data?.items ?? []
        : tasks.data?.items.filter((task) => matchesFilter(task.status, filter)) ?? [],
    [filter, scope, tasks.data]
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
        description={
          scope === "archived"
            ? "恢复误删任务、跟踪文件清理，或在媒体已清理后永久删除记录。"
            : "按处理轨道查看进度、失败点和重试结果。"
        }
        actions={
          <Link to="/tasks/new" className="button button-primary">
            新建任务
          </Link>
        }
      />

      <section className="task-scope-switch" aria-label="任务列表范围">
        <button
          type="button"
          className={scope === "active" ? "active" : ""}
          aria-pressed={scope === "active"}
          onClick={() => setSearchParams({})}
        >
          工作列表
        </button>
        <button
          type="button"
          className={scope === "archived" ? "active" : ""}
          aria-pressed={scope === "archived"}
          onClick={() => setSearchParams({ scope: "archived" })}
        >
          回收站
        </button>
      </section>

      {scope === "active" ? (
        <section className="task-control-strip" aria-label="任务筛选与批量操作">
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
          <div className="task-control-actions">
            <button
              className="button button-secondary"
              type="button"
              disabled={selected.length === 0 || bulkRetry.isPending}
              onClick={() => bulkRetry.mutate(selected)}
            >
              {bulkRetry.isPending
                ? "正在投递…"
                : selected.length > 0
                  ? `重试选中项 ${selected.length}`
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
        </section>
      ) : null}

      {notice ? (
        <TransientNotice
          tone={/失败|错误/.test(notice) ? "error" : "success"}
          onDismiss={() => setNotice("")}
        >
          {notice}
        </TransientNotice>
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
            ) : undefined
          }
        />
      ) : (
        <section className="work-panel task-list-panel" aria-label="任务列表">
          <div className="task-list-caption">
            <span>{visibleTasks.length} 条结果</span>
            <span>
              {scope === "archived"
                ? "永久删除前必须先完成媒体文件清理"
                : "运行中任务需先取消，再移入回收站"}
            </span>
          </div>
          <div className="track-list">
            {visibleTasks.map((task) => {
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
                <TaskTrack
                  task={task}
                  key={task.id}
                  selectable={scope === "active" && task.status === "failed"}
                  selected={selected.includes(task.id)}
                  onSelect={(checked) =>
                    setSelected((current) =>
                      checked
                        ? [...new Set([...current, task.id])]
                        : current.filter((taskID) => taskID !== task.id)
                    )
                  }
                  actions={
                    scope === "active" ? (
                      <>
                        <Link className="button button-secondary" to={`/tasks/${task.id}`}>
                          查看详情
                        </Link>
                        <button
                          className="button button-secondary"
                          type="button"
                          disabled={!archivableStatuses.has(task.status)}
                          title={
                            archivableStatuses.has(task.status)
                              ? "移入回收站"
                              : "请先取消并等待任务安全停止"
                          }
                          onClick={() => {
                            setActionError("");
                            setDialog({ mode: "archive", task });
                          }}
                        >
                          删除任务
                        </button>
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
                          恢复任务
                        </button>
                        {cleanupAvailable ? (
                          <button
                            className="button button-secondary"
                            type="button"
                            disabled={cleanupAssets.isPending}
                            onClick={() => cleanupAssets.mutate(task.id)}
                          >
                            清理文件
                          </button>
                        ) : cleanupPending ? (
                          <button className="button button-secondary" type="button" disabled>
                            文件清理中
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
                    )
                  }
                />
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
    </>
  );
}
