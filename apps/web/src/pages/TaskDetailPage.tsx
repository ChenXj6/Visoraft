import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, ApiError, type Task, type TaskStep } from "../api";
import {
  ConfirmDialog,
  LoadingBlock,
  PageHeader,
  PlatformChips,
  QueryError,
  ProgressBar,
  StatusBadge,
  WorkflowRail
} from "../components";
import {
  formatBytes,
  formatDateTime,
  formatDuration,
  assetKindLabel,
  errorCodeLabel,
  friendlyErrorMessage,
  mediaInfoParts,
  orderedTaskSteps,
  shortID,
  statusLabel,
  statusLabels,
  stepLabel,
  stepStatusLabel,
  taskStepProgress,
  subtitlePhaseLabels,
  taskStatusForDisplay
} from "../format";
import { DownloadStepActivity } from "./DownloadStepActivity";
import {
  TaskLifecycleDialog,
  type TaskLifecycleMode
} from "./TaskLifecycleDialog";

const assetStatusLabels: Record<string, string> = {
  available: "已校验",
  deleting: "正在清理",
  deleted: "已删除",
  failed: "清理失败"
};

const accessErrorCodes = new Set([
  "source_auth_required",
  "cookie_profile_unavailable",
  "cookie_file_invalid"
]);

function needsCookieRepair(task: Task) {
  return (
    accessErrorCodes.has(task.error_code ?? "") ||
    /sign in to confirm|not a bot|use --cookies|cookies-from-browser|login required/i.test(
      task.error_message ?? ""
    )
  );
}

function subtitleProgressText(step: TaskStep) {
  const phase = subtitlePhaseLabels[step.detail.phase ?? ""] ?? "正在处理字幕";
  const batchCount = step.detail.batch_count ?? 0;
  const batchIndex = step.detail.batch_index ?? 0;
  const completedBatches = step.detail.completed_batches ?? 0;
  const modelAttempt = step.detail.model_attempt ?? 0;
  const modelAttempts = step.detail.model_attempts ?? 0;
  const parts = [phase];
  if (batchCount > 0) {
    if (completedBatches >= batchIndex && completedBatches > 0) {
      parts.push(`已完成 ${completedBatches}/${batchCount} 批`);
    } else if (batchIndex > 0) {
      parts.push(`第 ${batchIndex}/${batchCount} 批`);
    }
  }
  if (modelAttempt > 0 && modelAttempts > 0) {
    parts.push(`请求 ${modelAttempt}/${modelAttempts}`);
  }
  if ((step.detail.sample_count ?? 0) > 0) {
    parts.push(
      `抽检 ${step.detail.sample_count}/${step.detail.total_count ?? step.detail.sample_count} 段`
    );
  }
  if ((step.detail.restored_items ?? 0) > 0) {
    parts.push(`已恢复 ${step.detail.restored_items} 段`);
  }
  if (step.detail.batch_split) parts.push("本批超时，已自动拆分");
  if (step.detail.repairing_missing) parts.push("正在补齐缺失译文");
  return parts.join(" · ");
}

function SubtitleDecisionNotice({ step }: { step: TaskStep }) {
  const decision = step.detail.decision;
  if (!decision) return null;
  const detection = decision.detection;
  const titles = {
    generated_subtitles: "已按原流程生成字幕",
    existing_soft_chinese: "已复用现有中文字幕",
    existing_hardcoded_chinese: "已保留画面中的中文字幕"
  } as const;
  const sourceLabels: Record<string, string> = {
    youtube_manual: "YouTube 人工字幕",
    youtube_auto: "YouTube 自动字幕",
    embedded: "媒体内嵌字幕轨",
    hardcoded: "视频画面硬字幕"
  };
  return (
    <div className="subtitle-decision-notice">
      <div>
        <strong>{titles[decision.disposition]}</strong>
        <span>{detection.reason || "字幕处理策略已记录"}</span>
      </div>
      {detection.state !== "disabled" && (
        <dl>
          <div>
            <dt>检测来源</dt>
            <dd>{sourceLabels[detection.source] ?? "未发现可复用字幕"}</dd>
          </div>
          {detection.confidence_percent > 0 && (
            <div>
              <dt>识别置信度</dt>
              <dd>{detection.confidence_percent}%</dd>
            </div>
          )}
          {detection.sample_count > 0 && (
            <div>
              <dt>画面抽检</dt>
              <dd>
                {detection.sample_count} 帧 · {detection.stable_pair_count} 组稳定字幕
              </dd>
            </div>
          )}
          <div>
            <dt>后续处理</dt>
            <dd>
              {decision.translation_skipped ? "跳过字幕翻译" : "继续字幕翻译"}
              {decision.burn_subtitles ? " · 继续烧录字幕" : " · 不重复烧录"}
            </dd>
          </div>
        </dl>
      )}
    </div>
  );
}

function actionErrorMessage(error: unknown, fallback: string) {
  return error instanceof ApiError ? error.message : fallback;
}

function taskNextStep(task: Task) {
  if (task.paused_at) {
    const pausedLabel = stepLabel(task.paused_step_kind || "");
    return {
      title: `${pausedLabel === "" ? "任务" : pausedLabel}已暂停`,
      description: "断点已保存，继续后自动恢复。",
      actionLabel: "",
      actionTo: ""
    };
  }
  const downloadStep = task.steps.find((step) => step.kind === "download");
  if (downloadStep?.detail.phase === "resume_requested") {
    return {
      title: "正在接续下载",
      description: "",
      actionLabel: "",
      actionTo: ""
    };
  }
  if (downloadStep?.status === "queued") {
    const isResume =
      downloadStep.attempt > 1 ||
      downloadStep.detail.phase === "paused" ||
      Number(downloadStep.detail.downloaded_bytes ?? 0) > 0;
    return {
      title: isResume ? "准备继续下载" : "准备下载",
      description: isResume ? "正在恢复已保存的下载断点。" : "正在等待下载服务接收任务。",
      actionLabel: "",
      actionTo: ""
    };
  }
  if (downloadStep?.status === "running") {
    return {
      title: "下载中",
      description: "下载进度已实时保存。",
      actionLabel: "",
      actionTo: ""
    };
  }
  switch (task.status) {
    case "awaiting_manual_review":
      return {
        title: "等待人工复核",
        description: "媒体、字幕和元数据已经准备好。完成复核后，任务会进入投稿准备。",
        actionLabel: "进入人工复核",
        actionTo: `/reviews/${task.id}`
      };
    case "ready_to_publish":
      return {
        title: "审核完成，等待投稿",
        description: "任务仍保存在任务中心。下一步是确认平台账号、分区、封面和投稿策略，然后加入发布队列。",
        actionLabel: "进入投稿准备",
        actionTo: `/publishing/${task.id}`
      };
    case "publishing":
      return {
        title: "平台正在处理投稿",
        description: "可在发布详情查看 AcFun 与 Bilibili 各自的上传进度、重试和远端稿件状态。",
        actionLabel: "查看发布进度",
        actionTo: `/publishing/${task.id}`
      };
    case "published":
    case "reconciled":
      if (task.publish_mode === "simulation") {
        return {
          title: "本地模拟已完成",
          description: "这条记录没有向 AcFun 或 Bilibili 提交，不能视为真实平台投稿。",
          actionLabel: "查看模拟记录",
          actionTo: `/publishing/${task.id}`
        };
      }
      return {
        title: task.status === "reconciled" ? "投稿结果已经对账" : "平台已接收投稿",
        description: "平台已返回稿件 ID；审核与公开状态仍以平台创作中心为准。",
        actionLabel: "查看发布结果",
        actionTo: `/publishing/${task.id}`
      };
    case "failed":
      return {
        title: "处理失败，需要恢复",
        description: "查看下方失败步骤和错误分类，修正输入后从失败步骤重试。",
        actionLabel: "",
        actionTo: ""
      };
    case "cancelled":
    case "abandoned":
      return {
        title: statusLabel(task.status),
        description: "任务记录仍会保留；如需继续，可恢复任务或重新创建。",
        actionLabel: "",
        actionTo: ""
      };
    default:
      return {
        title: statusLabels[task.status] ?? "正在处理",
        description: "后台正在执行当前步骤，刷新页面不会丢失进度。",
        actionLabel: "",
        actionTo: ""
      };
  }
}

function CopyButton({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);

  return (
    <button
      className="copy-button"
      type="button"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1_500);
        } catch {
          setCopied(false);
        }
      }}
    >
      {copied ? "已复制" : "复制"}
    </button>
  );
}

export default function TaskDetailPage() {
  const { taskId = "" } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [confirmCancel, setConfirmCancel] = useState(false);
  const [confirmCleanup, setConfirmCleanup] = useState(false);
  const [actionError, setActionError] = useState("");
  const [cookieSelection, setCookieSelection] = useState("");
  const [lifecycleMode, setLifecycleMode] = useState<TaskLifecycleMode | null>(null);

  const taskQuery = useQuery({
    queryKey: ["task", taskId],
    queryFn: () => api.task(taskId),
    enabled: Boolean(taskId),
    refetchInterval: 3_000
  });
  const cookieProfiles = useQuery({
    queryKey: ["cookie-profiles"],
    queryFn: api.cookieProfiles,
    refetchInterval: 15_000
  });

  useEffect(() => {
    if (taskQuery.data) {
      setCookieSelection(taskQuery.data.cookie_profile_id ?? "");
    }
  }, [taskQuery.data?.cookie_profile_id, taskQuery.data?.id]);

  const syncTask = (task: Task) => {
    queryClient.setQueryData(["task", taskId], task);
    void queryClient.invalidateQueries({ queryKey: ["tasks"] });
    void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
  };

  const cancelTask = useMutation({
    mutationFn: () => api.cancelTask(taskId),
    onSuccess: (task) => {
      setConfirmCancel(false);
      setActionError("");
      syncTask(task);
    },
    onError: (error) => setActionError(actionErrorMessage(error, "无法取消任务"))
  });

  const pauseTask = useMutation({
    mutationFn: () => api.pauseTask(taskId),
    onSuccess: (task) => {
      setActionError("");
      syncTask(task);
    },
    onError: (error) => setActionError(actionErrorMessage(error, "无法暂停任务"))
  });

  const resumeTask = useMutation({
    mutationFn: () => api.resumeTask(taskId),
    onSuccess: (task) => {
      setActionError("");
      syncTask(task);
    },
    onError: (error) => setActionError(actionErrorMessage(error, "无法继续任务"))
  });

  const retryTask = useMutation({
    mutationFn: () => api.retryTask(taskId),
    onSuccess: (task) => {
      setActionError("");
      syncTask(task);
    },
    onError: (error) => setActionError(actionErrorMessage(error, "无法重试任务"))
  });

  const repairAndRetry = useMutation({
    mutationFn: async () => {
      const current = taskQuery.data;
      if (!current) throw new Error("任务尚未加载");
      if ((current.cookie_profile_id ?? "") !== cookieSelection) {
        await api.setTaskCookieProfile(taskId, cookieSelection || undefined);
      }
      return api.retryTask(taskId);
    },
    onSuccess: (task) => {
      setActionError("");
      syncTask(task);
    },
    onError: (error) =>
      setActionError(actionErrorMessage(error, "Cookie 配置未能保存，任务没有重试"))
  });

  const deleteAssets = useMutation({
    mutationFn: () => api.deleteTaskAssets(taskId),
    onSuccess: (task) => {
      setConfirmCleanup(false);
      setActionError("");
      syncTask(task);
    },
    onError: (error) => setActionError(actionErrorMessage(error, "无法清理媒体文件"))
  });

  const lifecycleTask = useMutation({
    mutationFn: async ({
      mode,
      task,
      deleteAssets,
      reason
    }: {
      mode: TaskLifecycleMode;
      task: Task;
      deleteAssets: boolean;
      reason: string;
    }) => {
      if (mode === "archive") {
        await api.archiveTask(task.id, {
          expected_version: task.version,
          delete_assets: deleteAssets,
          reason
        });
        return "active" as const;
      }
      if (mode === "restore") {
        await api.restoreTask(task.id, {
          expected_version: task.version,
          reason
        });
        return "active" as const;
      }
      if (mode === "purge") {
        await api.purgeTask(task.id, {
          expected_version: task.version,
          confirmation: `purge:${task.id}`,
          reason
        });
        return "archived" as const;
      }
      throw new Error("不支持的任务操作");
    },
    onSuccess: async (destination) => {
      setLifecycleMode(null);
      setActionError("");
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["tasks"] }),
        queryClient.invalidateQueries({ queryKey: ["task-archive-preview"] }),
        queryClient.invalidateQueries({ queryKey: ["dashboard"] })
      ]);
      navigate(destination === "archived" ? "/tasks?scope=archived" : "/tasks");
    },
    onError: (error) =>
      setActionError(actionErrorMessage(error, "任务删除操作未能完成"))
  });

  const usableCookies = useMemo(
    () =>
      cookieProfiles.data?.items.filter((profile) => profile.has_usable_cookies) ?? [],
    [cookieProfiles.data]
  );

  if (taskQuery.isPending) {
    return <LoadingBlock label="正在读取任务详情" />;
  }
  if (taskQuery.isError) {
    return (
      <QueryError
        title="无法打开这条任务"
        message={taskQuery.error.message}
        retry={() => void taskQuery.refetch()}
      />
    );
  }

  const task = taskQuery.data;
  const isArchived = Boolean(task.archived_at);
  const archivable = [
    "awaiting_manual_review",
    "ready_to_publish",
    "published",
    "reconciled",
    "failed",
    "cancelled",
    "abandoned"
  ].includes(task.status);
  const requiresCookies = !isArchived && needsCookieRepair(task);
  const cancellable =
    !isArchived &&
    !["cancelled", "abandoned", "published", "reconciled"].includes(task.status);
  const pausable =
    !isArchived &&
    !task.paused_at &&
    [
      "queued",
      "fetching_metadata",
      "metadata_ready",
      "downloading",
      "processing",
      "awaiting_manual_review",
      "ready_to_publish"
    ].includes(task.status);
  const deletableAssets = task.assets.filter((asset) =>
    ["available", "failed"].includes(asset.status)
  );
  const selectedCookie = cookieProfiles.data?.items.find(
    (profile) => profile.id === task.cookie_profile_id
  );
  const inspectedDuration = task.assets.find(
    (asset) => asset.media_info?.duration_seconds !== undefined
  )?.media_info.duration_seconds;
  const displayDuration =
    task.duration_seconds ??
    (inspectedDuration === undefined ? undefined : Math.round(inspectedDuration));
  const nextStep = taskNextStep(task);
  const orderedSteps = orderedTaskSteps(task.steps);

  return (
    <>
      <PageHeader
        title={task.title || task.original_title || "正在读取媒体信息"}
        description={
          isArchived
            ? "这条任务位于回收站；处理和发布操作已锁定。"
            : task.paused_at
              ? `已暂停 · ${task.paused_step_kind ? stepLabel(task.paused_step_kind) : "当前节点"} · 断点已保存`
              : "任务状态、处理步骤和媒体文件都会保存，页面刷新不会丢失进度。"
        }
        actions={
          <div className="task-actions">
            {pausable && (
              <button
                type="button"
                className="button button-secondary"
                disabled={pauseTask.isPending}
                onClick={() => pauseTask.mutate()}
              >
                {pauseTask.isPending ? "正在暂停…" : "暂停处理"}
              </button>
            )}
            {!isArchived && task.paused_at && (
              <button
                type="button"
                className="button button-primary"
                disabled={resumeTask.isPending}
                onClick={() => resumeTask.mutate()}
              >
                {resumeTask.isPending ? "正在继续…" : "继续处理"}
              </button>
            )}
            {!isArchived &&
              (task.status === "failed" || task.status === "cancelled") &&
              !requiresCookies && (
              <button
                type="button"
                className="button button-primary"
                disabled={retryTask.isPending}
                onClick={() => retryTask.mutate()}
              >
                {retryTask.isPending
                  ? "正在恢复…"
                  : task.status === "cancelled"
                    ? "恢复任务"
                    : "重试失败步骤"}
              </button>
              )}
            {cancellable && (
              <button
                type="button"
                className="button button-danger"
                onClick={() => setConfirmCancel(true)}
              >
                取消任务
              </button>
            )}
            {!isArchived && (
              <button
                type="button"
                className="button button-secondary"
                disabled={!archivable || lifecycleTask.isPending}
                title={archivable ? "移入回收站" : "请先取消并等待任务安全停止"}
                onClick={() => {
                  setActionError("");
                  setLifecycleMode("archive");
                }}
              >
                删除任务
              </button>
            )}
            {isArchived && (
              <button
                type="button"
                className="button button-secondary"
                disabled={lifecycleTask.isPending}
                onClick={() => {
                  setActionError("");
                  setLifecycleMode("restore");
                }}
              >
                恢复任务
              </button>
            )}
            {isArchived && (
              <button
                type="button"
                className="button button-danger"
                disabled={task.assets.some((asset) => asset.status !== "deleted")}
                title={
                  task.assets.some((asset) => asset.status !== "deleted")
                    ? "请先清理全部媒体文件"
                    : "永久删除任务记录"
                }
                onClick={() => {
                  setActionError("");
                  setLifecycleMode("purge");
                }}
              >
                永久删除
              </button>
            )}
            <Link
              to={isArchived ? "/tasks?scope=archived" : "/tasks"}
              className="button button-secondary"
            >
              {isArchived ? "返回回收站" : "返回任务"}
            </Link>
          </div>
        }
      />

      {actionError && (
        <div className="action-error" role="alert">
          <strong>操作未完成</strong>
          <span>{actionError}</span>
        </div>
      )}

      {lifecycleMode ? (
        <TaskLifecycleDialog
          open
          mode={lifecycleMode}
          task={task}
          busy={lifecycleTask.isPending}
          error={actionError}
          onClose={() => {
            if (!lifecycleTask.isPending) {
              setLifecycleMode(null);
              setActionError("");
            }
          }}
          onConfirm={({ deleteAssets: removeAssets, reason }) =>
            lifecycleTask.mutate({
              mode: lifecycleMode,
              task,
              deleteAssets: removeAssets,
              reason
            })
          }
        />
      ) : null}

      {isArchived ? (
        <div className="inline-notice">
          <span>
            任务记录已移入回收站。可在回收站恢复、清理媒体文件或永久删除；远端平台稿件不受影响。
          </span>
          <Link to="/tasks?scope=archived">管理回收站</Link>
        </div>
      ) : null}

      <section className="work-panel task-master">
        <div className="task-master-copy">
          <div className="detail-status-line">
            <StatusBadge status={taskStatusForDisplay(task)} />
            <PlatformChips platforms={task.target_platforms} />
          </div>
          <div className="task-current-stage">
            <span>当前阶段</span>
            <h2>{nextStep.title}</h2>
            <p>{nextStep.description}</p>
            {!isArchived && nextStep.actionTo && (
              <Link className="button button-primary" to={nextStep.actionTo}>
                {nextStep.actionLabel}
              </Link>
            )}
          </div>
          <a
            href={task.source_url}
            target="_blank"
            rel="noreferrer"
            className="source-link"
            title={task.source_url}
          >
            {task.source_url}
          </a>
          <WorkflowRail task={task} />
        </div>

        <dl className="detail-summary">
          <div>
            <dt>任务 ID</dt>
            <dd>
              <code>{task.id}</code>
              <CopyButton value={task.id} />
            </dd>
          </div>
          <div>
            <dt>创建时间</dt>
            <dd>{formatDateTime(task.created_at)}</dd>
          </div>
          <div>
            <dt>最近更新</dt>
            <dd>{formatDateTime(task.updated_at)}</dd>
          </div>
          <div>
            <dt>媒体时长</dt>
            <dd>{formatDuration(displayDuration)}</dd>
          </div>
          <div>
            <dt>Cookie</dt>
            <dd>{selectedCookie?.name ?? (task.cookie_profile_id ? "配置已失效" : "未使用")}</dd>
          </div>
        </dl>
      </section>

      {task.error_message && (
        <section
          className={`failure-console ${requiresCookies ? "failure-console-access" : ""}`}
          aria-labelledby="failure-title"
        >
          <div className="failure-code">
            <span>错误代码</span>
            <strong>{errorCodeLabel(task.error_code || "")}</strong>
          </div>
          <div className="failure-copy">
            <h2 id="failure-title">
              {requiresCookies ? "目标网站要求登录 Cookie" : "处理步骤失败"}
            </h2>
            <p>{friendlyErrorMessage(task.error_message)}</p>
            <small>
              {requiresCookies
                ? "选择一组可用 Cookie 后保存并重试；转载声明不会绕过网站登录或机器人验证。"
                : task.error_retryable
                  ? "该错误允许从失败步骤重试。"
                  : "请先修正输入或运行配置，再重新执行。"}
            </small>
          </div>

          {requiresCookies && (
            <div className="failure-repair">
              <label className="field">
                <span>用于本任务的 Cookie</span>
                <select
                  value={cookieSelection}
                  onChange={(event) => setCookieSelection(event.target.value)}
                  disabled={cookieProfiles.isPending}
                >
                  <option value="">不使用 Cookie</option>
                  {usableCookies.map((profile) => (
                    <option key={profile.id} value={profile.id}>
                      {profile.name} · {profile.cookie_count} 条
                    </option>
                  ))}
                </select>
              </label>
              <div className="failure-repair-actions">
                <button
                  type="button"
                  className="button button-primary"
                  disabled={!cookieSelection || repairAndRetry.isPending}
                  onClick={() => repairAndRetry.mutate()}
                >
                  {repairAndRetry.isPending ? "正在保存并重试…" : "保存并重试"}
                </button>
                <Link className="button button-secondary" to="/cookies">
                  管理 Cookie
                </Link>
              </div>
              {usableCookies.length === 0 && !cookieProfiles.isPending && (
                <p className="field-error">尚无可用配置，请先上传 cookies.txt 或同步 CookieCloud。</p>
              )}
            </div>
          )}
        </section>
      )}

      <div className="detail-grid">
        <section className="work-panel detail-panel">
          <header className="work-panel-head">
            <div>
              <p className="eyebrow">执行记录</p>
              <h2>处理步骤</h2>
            </div>
          </header>
          {task.steps.length === 0 ? (
            <p className="quiet-empty">任务尚未生成执行步骤。</p>
          ) : (
            <ol className="step-timeline">
              {orderedSteps.map((step) => {
                const pausedStep = Boolean(task.paused_at && task.paused_step_kind === step.kind);
                const displayStatus = pausedStep ? "paused" : step.status;
                const displayProgress = taskStepProgress(step);
                const showProgress = pausedStep || !["queued", "cancelled"].includes(step.status);
                const queuedDownload = step.kind === "download" && step.status === "queued";
                const resumeRequested =
                  step.kind === "download" && step.detail.phase === "resume_requested";
                const queuedDownloadCopy =
                  step.attempt > 1 ||
                  step.detail.phase === "paused" ||
                  Number(step.detail.downloaded_bytes ?? 0) > 0
                    ? "准备恢复下载"
                    : "准备下载";
                return (
                <li className={`step-item step-${displayStatus}`} key={step.kind}>
                  <span className="step-marker" aria-hidden="true" />
                  <div className="step-copy">
                    <div className="step-title-line">
                      <strong>{stepLabel(step.kind)}</strong>
                      <span>{pausedStep ? "已暂停" : resumeRequested ? "正在接续" : stepStatusLabel(step.status)}</span>
                    </div>
                    {step.kind !== "download" && showProgress && (
                      <ProgressBar
                        value={displayProgress}
                        label={`${stepLabel(step.kind)}进度`}
                        tone={pausedStep ? "paused" : step.status === "failed" ? "danger" : step.status === "succeeded" ? "success" : "primary"}
                        compact
                      />
                    )}
                    <p>
                      {step.status === "queued" && !pausedStep
                        ? queuedDownload
                          ? queuedDownloadCopy
                          : "等待前序步骤完成"
                        : `更新于 ${formatDateTime(step.updated_at)}`}
                    </p>
                    {step.kind === "subtitles" && step.detail.phase && (
                      <p className="step-phase" aria-live="polite">
                        {subtitleProgressText(step)}
                      </p>
                    )}
                    {step.kind === "subtitles" && step.status === "succeeded" && (
                      <SubtitleDecisionNotice step={step} />
                    )}
                    {step.kind === "download" && (
                      <DownloadStepActivity step={step} paused={pausedStep} />
                    )}
                    {step.error_message && (
                      <p className="inline-error">{friendlyErrorMessage(step.error_message)}</p>
                    )}
                  </div>
                  <strong className="step-progress">
                    {pausedStep
                      ? "已暂停"
                      : step.status === "succeeded"
                      ? "完成"
                      : step.kind === "download" && step.status === "running"
                        ? resumeRequested ? "正在接续" : "下载中"
                        : step.status === "queued"
                          ? "等待"
                          : `${displayProgress.toFixed(displayProgress < 10 ? 1 : 0)}%`}
                  </strong>
                </li>
              )})}
            </ol>
          )}
        </section>

        <section className="work-panel detail-panel task-input-panel">
          <header className="work-panel-head">
            <div>
              <p className="eyebrow">任务输入</p>
              <h2>执行参数</h2>
            </div>
          </header>
          <dl className="record-list">
            <div className="record-span">
              <dt>视频 URL</dt>
              <dd>
                <a href={task.source_url} target="_blank" rel="noreferrer">
                  {task.source_url}
                </a>
              </dd>
            </div>
            <div>
              <dt>提取器</dt>
              <dd>{task.extractor || "等待识别"}</dd>
            </div>
            <div>
              <dt>媒体时长</dt>
              <dd>{formatDuration(displayDuration)}</dd>
            </div>
            <div className="record-span">
              <dt>Cookie 配置</dt>
              <dd>{selectedCookie?.name ?? "未使用"}</dd>
            </div>
          </dl>
        </section>
      </div>

      <section className="work-panel asset-panel">
        <header className="work-panel-head">
          <div>
            <p className="eyebrow">任务文件</p>
            <h2>下载与处理结果</h2>
          </div>
          <div className="asset-heading-actions">
            <span className="version-tag">
              {task.assets.filter((asset) => asset.status === "available").length} 个可用
            </span>
            {deletableAssets.length > 0 &&
              !["published", "reconciled"].includes(task.status) && (
                <button
                  type="button"
                  className="button button-danger"
                  onClick={() => setConfirmCleanup(true)}
                >
                  清理文件
                </button>
              )}
          </div>
        </header>

        {task.assets.length === 0 ? (
          <div className="asset-empty">
            <span>暂无文件</span>
            <div>
              <strong>尚未生成任务文件</strong>
              <p>下载完成后，这里会显示文件位置、大小和完整性结果。</p>
            </div>
          </div>
        ) : (
          <div className="asset-table-wrap">
            <table className="asset-table">
              <thead>
                <tr>
                  <th>文件</th>
                  <th>状态</th>
                  <th>类型 / 大小</th>
                  <th>保存位置</th>
                  <th>完整性</th>
                </tr>
              </thead>
              <tbody>
                {task.assets.map((asset) => {
                  const inspectedParts = mediaInfoParts(asset.media_info);
                  return (
                    <tr key={asset.id}>
                      <td>
                        {asset.status === "available" ? (
                          <a
                            href={api.assetContentURL(task.id, asset.id)}
                            target="_blank"
                            rel="noreferrer"
                          >
                            <strong>{asset.original_name}</strong>
                          </a>
                        ) : (
                          <strong>{asset.original_name}</strong>
                        )}
                        <small>{assetKindLabel(asset.kind)}</small>
                      </td>
                      <td>
                        <span className={`asset-state asset-state-${asset.status}`}>
                          {assetStatusLabels[asset.status] ?? "未知状态"}
                        </span>
                        {asset.error_message && (
                          <small className="asset-error">
                            {friendlyErrorMessage(asset.error_message)}
                          </small>
                        )}
                      </td>
                      <td>
                        {asset.content_type}
                        <small>{formatBytes(asset.size_bytes)}</small>
                        {inspectedParts.length > 0 && (
                          <span className="media-info-tags" aria-label="媒体检测结果">
                            {inspectedParts.map((part) => (
                              <span key={part}>{part}</span>
                            ))}
                          </span>
                        )}
                      </td>
                      <td>
                        <code>任务文件 / {task.id.slice(0, 8)} / {asset.original_name}</code>
                      </td>
                      <td>
                        {asset.checksum_sha256 ? (
                          <span className="asset-state asset-state-available" title={asset.checksum_sha256}>
                            已校验
                          </span>
                        ) : "—"}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="work-panel statement-panel">
        <header className="work-panel-head">
          <div>
            <p className="eyebrow">发布备注</p>
            <h2>双版转载声明</h2>
          </div>
          <span className="version-tag">
            默认{task.repost_statement_version === "brief_v1" ? "简版" : "完整版"}
          </span>
        </header>
        <div className="statement-grid">
          <article
            className={
              task.repost_statement_version === "brief_v1" ? "statement-active" : ""
            }
          >
            <div>
              <strong>简版</strong>
              <CopyButton value={task.repost_statement_brief} />
            </div>
            <p>{task.repost_statement_brief}</p>
          </article>
          <article
            className={task.repost_statement_version === "full_v1" ? "statement-active" : ""}
          >
            <div>
              <strong>完整版</strong>
              <CopyButton value={task.repost_statement_full} />
            </div>
            <p>{task.repost_statement_full}</p>
          </article>
        </div>
        <p className="statement-warning">
          转载声明是发布文案，不会绕过网站登录、机器人验证或平台投稿规则。
        </p>
      </section>

      <ConfirmDialog
        open={confirmCancel}
        title="取消这条任务？"
        description="排队或运行中的步骤会停止；下载会在下一次进度检查时退出。已生成的媒体文件不会自动删除。"
        confirmLabel="确认取消"
        destructive
        busy={cancelTask.isPending}
        onConfirm={() => cancelTask.mutate()}
        onClose={() => {
          if (!cancelTask.isPending) setConfirmCancel(false);
        }}
      />

      <ConfirmDialog
        open={confirmCleanup}
        title="删除这条任务的媒体文件？"
        description={`将永久删除 ${deletableAssets.length} 个媒体文件。任务和文件清单会保留，但文件内容无法恢复。`}
        confirmLabel="确认清理"
        destructive
        busy={deleteAssets.isPending}
        onConfirm={() => deleteAssets.mutate()}
        onClose={() => {
          if (!deleteAssets.isPending) setConfirmCleanup(false);
        }}
      />
    </>
  );
}
