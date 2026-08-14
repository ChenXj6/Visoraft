import type { TaskStep } from "../api";
import { ProgressBar } from "../components";
import { formatBytes, formatDuration, formatTransferRate, taskStepProgress } from "../format";

function finitePositive(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value) && value > 0;
}

export function DownloadStepActivity({ step, paused = false }: { step: TaskStep; paused?: boolean }) {
  const detail = step.detail ?? {};
  const downloaded = finitePositive(detail.downloaded_bytes)
    ? detail.downloaded_bytes
    : 0;
  const total = finitePositive(detail.total_bytes) ? detail.total_bytes : 0;
  const speed = finitePositive(detail.speed_bytes_per_second)
    ? detail.speed_bytes_per_second
    : 0;
  const eta = finitePositive(detail.eta_seconds) ? detail.eta_seconds : 0;
  const displayTotal = Math.max(downloaded, total);
  const progress = taskStepProgress(step);
  const hasCheckpoint = downloaded > 0 || total > 0;
  const queued = step.status === "queued" && !paused;
  const resumeRequested = detail.phase === "resume_requested";
  const resuming = resumeRequested || (queued && (
    hasCheckpoint || detail.phase === "paused" || step.attempt > 1
  ));
  if (step.status !== "running" && !paused && !queued) return null;
  const progressKnown = total > 0;
  const stateText = paused ? "已暂停" : resuming ? "正在接续" : queued ? "准备下载" : "下载中";

  return (
    <div
      className={`download-activity ${paused ? "download-activity-paused" : ""}`}
      role="status"
      aria-live="polite"
      aria-label="实时下载信息"
    >
      <div className="download-progress-row">
        <ProgressBar
          value={progress}
          label="源文件下载进度"
          tone={paused ? "paused" : "primary"}
          indeterminate={!progressKnown && !paused}
        />
        <strong>{progressKnown ? `${progress.toFixed(1)}%` : stateText}</strong>
      </div>
      <dl className="download-inline-metrics">
        <div>
          <dt>速度</dt>
          <dd>{paused ? "已暂停" : resuming ? "正在接续" : queued ? "等待中" : formatTransferRate(speed)}</dd>
        </div>
        <div>
          <dt>大小</dt>
          <dd>
            {downloaded > 0 ? formatBytes(downloaded) : "—"}
            {displayTotal > 0 ? ` / ${formatBytes(displayTotal)}` : ""}
          </dd>
        </div>
        <div>
          <dt>剩余</dt>
          <dd>{paused || queued ? "—" : eta > 0 ? `约 ${formatDuration(Math.round(eta))}` : "计算中"}</dd>
        </div>
      </dl>
    </div>
  );
}
