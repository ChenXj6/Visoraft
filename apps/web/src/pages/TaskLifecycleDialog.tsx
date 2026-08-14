import { useEffect, useMemo, useState } from "react";
import type { Task, TaskArchivePreview } from "../api";
import { ConfirmDialog } from "../components";
import { formatBytes, shortID } from "../format";

export type TaskLifecycleMode = "archive" | "archive_all" | "restore" | "purge";

export function TaskLifecycleDialog({
  open,
  mode,
  task,
  preview,
  busy,
  error,
  onClose,
  onConfirm
}: {
  open: boolean;
  mode: TaskLifecycleMode;
  task?: Task;
  preview?: TaskArchivePreview;
  busy?: boolean;
  error?: string;
  onClose: () => void;
  onConfirm: (input: { deleteAssets: boolean; reason: string }) => void;
}) {
  const [deleteAssets, setDeleteAssets] = useState(false);
  const [reason, setReason] = useState("");
  const [confirmed, setConfirmed] = useState(false);

  useEffect(() => {
    if (!open) return;
    setDeleteAssets(false);
    setReason("");
    setConfirmed(false);
  }, [mode, open, task?.id]);

  const remainingAssets = useMemo(
    () => task?.assets.filter((asset) => asset.status !== "deleted") ?? [],
    [task?.assets]
  );
  const isArchive = mode === "archive" || mode === "archive_all";
  const purgeBlocked = mode === "purge" && remainingAssets.length > 0;
  const reasonValid = reason.trim().length >= 4 && reason.trim().length <= 500;

  const copy = {
    archive: {
      title: "将这条任务移入回收站？",
      description: `任务 #${task ? shortID(task.id) : ""} 会从工作列表移除。远端平台稿件不会下架。`,
      confirmLabel: "移入回收站"
    },
    archive_all: {
      title: "清空当前任务列表？",
      description: `本次范围包含 ${preview?.total_tasks ?? 0} 条任务；运行中的任务会保留并报告原因。`,
      confirmLabel: "确认清空"
    },
    restore: {
      title: "恢复这条任务？",
      description: "任务会重新出现在工作列表中；已清理的媒体文件不会自动恢复。",
      confirmLabel: "恢复任务"
    },
    purge: {
      title: "永久删除任务记录？",
      description: "该操作会删除任务、步骤、审核和投稿历史，无法撤销；远端平台稿件不会下架。",
      confirmLabel: "永久删除"
    }
  }[mode];

  return (
    <ConfirmDialog
      open={open}
      title={copy.title}
      description={copy.description}
      confirmLabel={copy.confirmLabel}
      busy={busy}
      destructive={mode !== "restore"}
      confirmDisabled={!confirmed || !reasonValid || purgeBlocked}
      onClose={onClose}
      onConfirm={() => onConfirm({ deleteAssets, reason: reason.trim() })}
    >
      {mode === "archive_all" && preview ? (
        <dl className="task-impact-grid" aria-label="清空影响范围">
          <div>
            <dt>全部任务</dt>
            <dd>{preview.total_tasks}</dd>
          </div>
          <div>
            <dt>可移入回收站</dt>
            <dd>{preview.archivable_tasks}</dd>
          </div>
          <div>
            <dt>仍在运行</dt>
            <dd>{preview.running_tasks}</dd>
          </div>
          <div>
            <dt>媒体文件</dt>
            <dd>
              {preview.asset_count} 个 · {formatBytes(preview.asset_bytes)}
            </dd>
          </div>
        </dl>
      ) : null}

      {isArchive ? (
        <label className="task-cleanup-choice">
          <input
            type="checkbox"
            checked={deleteAssets}
            disabled={busy}
            onChange={(event) => setDeleteAssets(event.target.checked)}
          />
          <span>
            <strong>同时清理媒体文件</strong>
            <small>
                    相关媒体文件会在后台清理；任务先进入回收站，可继续查看清理结果。
            </small>
          </span>
        </label>
      ) : null}

      {mode === "purge" && purgeBlocked ? (
        <div className="task-lifecycle-warning" role="alert">
          <strong>还有 {remainingAssets.length} 个媒体文件未清理</strong>
          <span>先在回收站执行“清理文件”，待状态完成后才能永久删除记录。</span>
        </div>
      ) : null}

      <label className="field">
        <span>操作原因</span>
        <textarea
          rows={3}
          maxLength={500}
          value={reason}
          disabled={busy}
          placeholder="记录本次操作的原因（至少 4 个字符）"
          aria-invalid={reason.length > 0 && !reasonValid}
          onChange={(event) => setReason(event.target.value)}
        />
        <small>{reason.length}/500</small>
      </label>

      <label className="task-lifecycle-confirm">
        <input
          type="checkbox"
          checked={confirmed}
          disabled={busy || purgeBlocked}
          onChange={(event) => setConfirmed(event.target.checked)}
        />
        <span>
          {mode === "purge"
            ? "我理解任务记录将永久删除，且这不会下架远端稿件"
            : mode === "restore"
              ? "我确认把该任务恢复到工作列表"
              : "我已核对影响范围，并确认执行"}
        </span>
      </label>

      {error ? (
        <div className="task-lifecycle-error" role="alert">
          {error}
        </div>
      ) : null}
    </ConfirmDialog>
  );
}
