import type { TaskStep } from "../api";
import { formatBytes, formatDuration } from "../format";

function finitePositive(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value) && value > 0;
}

export function DownloadStepActivity({ step }: { step: TaskStep }) {
  if (step.status !== "running") return null;

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

  return (
    <dl
      className="download-inline-metrics"
      role="status"
      aria-live="polite"
      aria-label="实时下载信息"
    >
      <div>
        <dt>速度</dt>
        <dd>{speed > 0 ? `${formatBytes(speed)}/秒` : "采样中"}</dd>
      </div>
      <div>
        <dt>文件大小</dt>
        <dd>
          {displayTotal > 0 ? formatBytes(displayTotal) : "获取中"}
          {detail.total_bytes_is_estimate ? "（估算）" : ""}
        </dd>
      </div>
      <div>
        <dt>预计剩余</dt>
        <dd>{eta > 0 ? `约 ${formatDuration(Math.round(eta))}` : "计算中"}</dd>
      </div>
    </dl>
  );
}
