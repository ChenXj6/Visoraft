import { useEffect, useRef, type ReactNode } from "react";
import { Link } from "react-router-dom";
import type { Task, TaskStep } from "./api";
import {
  formatRelativeTime,
  friendlyErrorMessage,
  platformLabel,
  shortID,
  statusLabel,
  statusTone,
  taskStatusForDisplay
} from "./format";
import { Icon } from "./icons";

export function PageHeader({
  eyebrow,
  title,
  description,
  actions
}: {
  eyebrow?: string;
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <header className="page-header">
      <div className="page-heading">
        {eyebrow && <p className="eyebrow">{eyebrow}</p>}
        <h1 title={title}>{title}</h1>
        {description && <p className="page-description">{description}</p>}
      </div>
      {actions && <div className="page-actions">{actions}</div>}
    </header>
  );
}

export function StatusBadge({ status }: { status: string }) {
  return (
    <span className={`status-badge status-${statusTone(status)}`}>
      <span className="status-dot" aria-hidden="true" />
      {statusLabel(status)}
    </span>
  );
}

export function PlatformChips({ platforms }: { platforms: string[] }) {
  return (
    <span className="chip-row" aria-label="目标平台">
      {platforms.map((platform) => (
        <span className={`platform-chip platform-${platform}`} key={platform}>
          {platformLabel(platform)}
        </span>
      ))}
    </span>
  );
}

export function TaskTitle({ task }: { task: Task }) {
  return <>{task.title || task.original_title || "正在读取媒体信息"}</>;
}

export function currentStep(task: Task): TaskStep | undefined {
  return (
    task.steps.find((step) => step.status === "running") ??
    task.steps.find((step) => step.status === "failed") ??
    task.steps.find((step) => step.status === "queued") ??
    task.steps.at(-1)
  );
}

type RailState = "done" | "active" | "error" | "waiting" | "idle" | "stopped";

const workflowStages = [
  { key: "metadata", label: "信息" },
  { key: "download", label: "下载" },
  { key: "process", label: "处理" },
  { key: "review", label: "复核" },
  { key: "publish", label: "发布" }
] as const;

function persistedStepState(task: Task, kind: string): RailState | undefined {
  const step = task.steps.find((item) => item.kind === kind);
  if (!step) return undefined;
  if (step.status === "succeeded" || step.status === "skipped") return "done";
  if (step.status === "running") return "active";
  if (step.status === "failed") return "error";
  if (step.status === "cancelled") return "stopped";
  return "waiting";
}

function stageState(task: Task, key: (typeof workflowStages)[number]["key"]): RailState {
  if (key === "metadata" || key === "download") {
    const persisted = persistedStepState(task, key);
    if (persisted) return persisted;
  }
  const status = task.status;
  if (status === "cancelled" || status === "abandoned") return "stopped";
  if (key === "process") {
    const persisted = persistedStepState(task, "media_inspect");
    if (persisted) return persisted;
    if (status === "processing") return "active";
    if (
      ["awaiting_manual_review", "ready_to_publish", "publishing", "published", "reconciled"].includes(
        status
      )
    ) {
      return "done";
    }
  }
  if (key === "review") {
    if (status === "awaiting_manual_review") return "active";
    if (["ready_to_publish", "publishing", "published", "reconciled"].includes(status)) {
      return "done";
    }
  }
  if (key === "publish") {
    if (status === "publishing") return "active";
    if (["published", "reconciled"].includes(status)) return "done";
  }
  return "idle";
}

export function WorkflowRail({ task, compact = false }: { task: Task; compact?: boolean }) {
  return (
    <ol
      className={`workflow-rail ${compact ? "workflow-rail-compact" : ""}`}
      aria-label="任务处理轨道"
    >
      {workflowStages.map((stage) => {
        const state = stageState(task, stage.key);
        return (
          <li className={`rail-stage rail-${state}`} key={stage.key}>
            <span className="rail-line" aria-hidden="true" />
            <span className="rail-node" aria-hidden="true" />
            <span className="rail-label">
              {stage.label}
              <span className="sr-only">
                ：
                {state === "done"
                  ? "完成"
                  : state === "active"
                    ? "执行中"
                    : state === "error"
                      ? "失败"
                      : state === "stopped"
                        ? "已停止"
                        : "未开始"}
              </span>
            </span>
          </li>
        );
      })}
    </ol>
  );
}

export function TaskTrack({
  task,
  selectable,
  selected,
  onSelect,
  actions
}: {
  task: Task;
  selectable?: boolean;
  selected?: boolean;
  onSelect?: (selected: boolean) => void;
  actions?: ReactNode;
}) {
  const step = currentStep(task);
  return (
    <article className={`task-track ${selected ? "task-track-selected" : ""}`}>
      <div className="track-media">
        {task.thumbnail_url ? (
          <img src={task.thumbnail_url} alt="" loading="lazy" />
        ) : (
          <span className="track-media-fallback" aria-hidden="true">
            <span><Icon name="media" /></span>
          </span>
        )}
        {selectable ? (
          <label className="track-selector">
            <input
              type="checkbox"
              checked={Boolean(selected)}
              onChange={(event) => onSelect?.(event.target.checked)}
              aria-label={`选择任务 ${shortID(task.id)}`}
            />
            <span aria-hidden="true" />
          </label>
        ) : null}
      </div>

      <div className="track-main">
        <div className="track-title-line">
          <div>
            <p className="track-kicker">
              <span className="timecode">#{shortID(task.id)}</span>
              <span>{formatRelativeTime(task.updated_at)}</span>
            </p>
            <h2>
              <Link to={`/tasks/${task.id}`}>
                <TaskTitle task={task} />
              </Link>
            </h2>
          </div>
          <div className="track-tags">
            <PlatformChips platforms={task.target_platforms} />
            <StatusBadge status={taskStatusForDisplay(task)} />
          </div>
        </div>
        <a
          className="track-source"
          href={task.source_url}
          target="_blank"
          rel="noreferrer"
          title={task.source_url}
        >
          {task.source_url}
        </a>
        <div className="track-progress-line">
          <WorkflowRail task={task} compact />
          <span className="track-progress-copy">
            {step ? `${step.progress}% · 尝试 ${step.attempt}` : "等待步骤"}
          </span>
        </div>
        {task.error_message && (
          <p className="track-error">{friendlyErrorMessage(task.error_message)}</p>
        )}
        {actions ? <div className="track-actions">{actions}</div> : null}
      </div>

    </article>
  );
}

export function QueryError({
  title = "暂时无法读取数据",
  message,
  retry
}: {
  title?: string;
  message?: string;
  retry?: () => void;
}) {
  return (
    <div className="state-panel state-error" role="alert">
      <span className="state-code" aria-hidden="true">
        !
      </span>
      <div>
        <h2>{title}</h2>
        <p>{message ?? "请检查本地服务后重试。"}</p>
      </div>
      {retry && (
        <button className="button button-secondary" type="button" onClick={retry}>
          重新加载
        </button>
      )}
    </div>
  );
}

export function EmptyState({
  title,
  description,
  action
}: {
  title: string;
  description: string;
  action?: ReactNode;
}) {
  return (
    <div className="empty-state">
      <span className="empty-code" aria-hidden="true">
        —
      </span>
      <h2>{title}</h2>
      <p>{description}</p>
      {action}
    </div>
  );
}

export function LoadingBlock({ label = "正在加载" }: { label?: string }) {
  return (
    <div className="loading-block" aria-busy="true" aria-label={label}>
      <span />
      <span />
      <span />
      <p>{label}</p>
    </div>
  );
}

export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel,
  busy,
  destructive = false,
  confirmDisabled = false,
  children,
  onConfirm,
  onClose
}: {
  open: boolean;
  title: string;
  description: string;
  confirmLabel: string;
  busy?: boolean;
  destructive?: boolean;
  confirmDisabled?: boolean;
  children?: ReactNode;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    if (open && !dialog.open) {
      dialog.showModal();
      cancelRef.current?.focus();
    } else if (!open && dialog.open) {
      dialog.close();
    }
  }, [open]);

  return (
    <dialog
      ref={dialogRef}
      className="confirm-dialog"
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      onClose={onClose}
    >
      <div className="dialog-head">
        <span className="dialog-mark" aria-hidden="true">
          !
        </span>
        <div>
          <h2>{title}</h2>
          <p>{description}</p>
        </div>
      </div>
      {children ? <div className="dialog-body">{children}</div> : null}
      <div className="dialog-actions">
        <button
          ref={cancelRef}
          className="button button-secondary"
          type="button"
          disabled={busy}
          onClick={onClose}
        >
          返回
        </button>
        <button
          className={`button ${destructive ? "button-danger" : "button-primary"}`}
          type="button"
          disabled={busy || confirmDisabled}
          onClick={onConfirm}
        >
          {busy ? "正在处理…" : confirmLabel}
        </button>
      </div>
    </dialog>
  );
}
