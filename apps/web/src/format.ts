import type { MediaInfo, Task } from "./api";

const dateTimeFormatter = new Intl.DateTimeFormat("zh-CN", {
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  hour12: false
});

const relativeFormatter = new Intl.RelativeTimeFormat("zh-CN", { numeric: "auto" });

export function formatDateTime(value?: string): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return dateTimeFormatter.format(date);
}

export function formatRelativeTime(value: string): string {
  const date = new Date(value);
  const seconds = Math.round((date.getTime() - Date.now()) / 1_000);
  const absolute = Math.abs(seconds);
  if (absolute < 60) return relativeFormatter.format(seconds, "second");
  if (absolute < 3_600) return relativeFormatter.format(Math.round(seconds / 60), "minute");
  if (absolute < 86_400) return relativeFormatter.format(Math.round(seconds / 3_600), "hour");
  return relativeFormatter.format(Math.round(seconds / 86_400), "day");
}

export function formatDuration(seconds?: number): string {
  if (seconds === undefined) return "—";
  const hours = Math.floor(seconds / 3_600);
  const minutes = Math.floor((seconds % 3_600) / 60);
  const rest = seconds % 60;
  return [hours, minutes, rest]
    .filter((_, index) => index > 0 || hours > 0)
    .map((part) => String(part).padStart(2, "0"))
    .join(":");
}

const videoTypeLabels: Record<string, string> = {
  video: "常规视频",
  short: "短视频",
  live: "直播"
};

export function videoTypeLabel(type: string): string {
  return videoTypeLabels[type] ?? "未知类型";
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = bytes / 1024;
  let unit = units[0];
  for (let index = 1; index < units.length && value >= 1024; index += 1) {
    value /= 1024;
    unit = units[index];
  }
  return `${value >= 10 ? value.toFixed(1) : value.toFixed(2)} ${unit}`;
}

export function mediaInfoParts(info?: MediaInfo): string[] {
  if (!info || info.schema_version !== 1) return [];
  const parts: string[] = [];
  if (info.width && info.height) parts.push(`${info.width}×${info.height}`);
  const codecs = [info.video_codec, info.audio_codec]
    .filter((value): value is string => Boolean(value))
    .map((value) => value.toUpperCase());
  if (codecs.length > 0) parts.push(codecs.join(" / "));
  const frameRate = parseFrameRate(info.frame_rate);
  if (frameRate) parts.push(`${frameRate} FPS`);
  if (info.sample_rate) {
    const sampleRate = info.sample_rate >= 1_000
      ? `${(info.sample_rate / 1_000).toFixed(info.sample_rate % 1_000 === 0 ? 0 : 1)} kHz`
      : `${info.sample_rate} Hz`;
    parts.push(info.channels ? `${sampleRate} · ${info.channels}ch` : sampleRate);
  }
  if (info.stream_count) parts.push(`${info.stream_count} STREAMS`);
  return parts;
}

function parseFrameRate(value?: string): string {
  if (!value) return "";
  const matched = /^(\d+)\/(\d+)$/.exec(value);
  if (!matched) return value.slice(0, 20);
  const numerator = Number(matched[1]);
  const denominator = Number(matched[2]);
  if (!denominator) return "";
  const result = numerator / denominator;
  return Number.isInteger(result) ? String(result) : result.toFixed(2);
}

export function shortID(id: string): string {
  return id.slice(0, 8);
}

export const platformLabels: Record<string, string> = {
  acfun: "AcFun",
  bilibili: "Bilibili"
};

export const statusLabels: Record<string, string> = {
  queued: "等待处理",
  fetching_metadata: "获取信息",
  metadata_ready: "信息已获取",
  downloading: "下载中",
  processing: "处理中",
  awaiting_manual_review: "等待人工复核",
  ready_to_publish: "等待发布",
  publishing: "发布中",
  published: "投稿已提交",
  submitted: "平台已接收",
  simulated: "本地模拟完成",
  reconciled: "已对账",
  draft: "草稿",
  blocked: "存在阻塞",
  partial_success: "部分成功",
  submitting: "正在提交",
  uploading: "正在上传",
  reconciliation_required: "等待回查",
  uncertain: "结果待确认",
  completed: "已完成",
  succeeded: "已完成",
  running: "执行中",
  unchecked: "未校验",
  checking: "正在校验",
  syncing: "正在同步",
  ready: "可用",
  expired: "登录已过期",
  error: "异常",
  archived: "已归档",
  disabled: "已停用",
  failed: "处理失败",
  cancelled: "已取消",
  abandoned: "已放弃"
};

export function statusTone(status: string): string {
  if (["failed", "abandoned", "error", "expired", "blocked"].includes(status)) {
    return "danger";
  }
  if (
    [
      "published",
      "submitted",
      "reconciled",
      "metadata_ready",
      "completed",
      "succeeded",
      "ready"
    ].includes(status)
  ) {
    return "success";
  }
  if (
    [
      "queued",
      "awaiting_manual_review",
      "draft",
      "partial_success",
      "reconciliation_required",
      "uncertain",
      "unchecked"
    ].includes(status)
  ) {
    return "warning";
  }
  if (["cancelled", "archived", "disabled"].includes(status)) return "muted";
  return "running";
}

export function taskStatusForDisplay(task: Task): string {
  if (
    ["published", "reconciled"].includes(task.status) &&
    task.publish_mode === "simulation"
  ) {
    return "simulated";
  }
  return task.status;
}

export const stepLabels: Record<string, string> = {
  metadata: "读取媒体信息",
  download: "下载源文件",
  media_inspect: "检测媒体",
  transcode: "转码",
  moderation: "内容安全审核",
  ai_metadata: "生成文案",
  asr: "识别字幕",
  subtitles: "字幕处理",
  review: "审核",
  publish: "发布"
};

export const stepStatusLabels: Record<string, string> = {
  queued: "排队",
  running: "执行中",
  succeeded: "完成",
  failed: "失败",
  cancelled: "已取消",
  skipped: "已跳过"
};

export const subtitlePhaseLabels: Record<string, string> = {
  existing_subtitle_detection_started: "正在检查已有中文字幕",
  existing_subtitle_ocr_started: "正在检查画面字幕",
  existing_subtitle_ocr_sampling: "正在抽帧识别画面字幕",
  existing_soft_subtitle_found: "已找到可复用的中文字幕",
  existing_hardcoded_subtitle_kept: "已保留画面中的中文字幕",
  existing_subtitle_detection_finished: "已有字幕检查完成",
  existing_chinese_translation_skipped: "已有中文字幕，已跳过翻译",
  completed: "字幕处理完成",
  source_ready: "源媒体已准备",
  audio_extracting: "正在提取音频",
  asr_upload_preparing: "正在准备上传音频",
  asr_uploaded: "音频已上传",
  asr_waiting: "等待语音识别结果",
  asr_completed: "语音识别完成",
  asr_checkpoint_reused: "已复用语音识别结果",
  subtitle_postprocessing: "正在清理字幕",
  smart_segmentation: "正在智能分段",
  smart_segmentation_checkpoint_reused: "已复用智能分段结果",
  segmentation_completed: "字幕分段完成",
  subtitle_translation: "正在翻译字幕",
  subtitle_translation_checkpoint_reused: "已复用部分翻译结果",
  subtitle_quality_check: "正在检查字幕质量",
  subtitle_artifact_saving: "正在保存字幕文件"
};

export function stepLabel(kind: string): string {
  return stepLabels[kind] ?? "未知处理步骤";
}

export function stepStatusLabel(status: string): string {
  return stepStatusLabels[status] ?? "未知状态";
}

export function statusLabel(status: string): string {
  return statusLabels[status] ?? "未知状态";
}

export function platformLabel(platform: string): string {
  return platformLabels[platform] ?? "未知平台";
}

const assetKindLabels: Record<string, string> = {
  source: "源媒体",
  thumbnail: "视频封面",
  transcoded: "转码视频",
  subtitle_original_vtt: "原文字幕",
  subtitle_original_srt: "原文字幕",
  subtitle_original_qc: "原文字幕质检报告",
  subtitle_translated_vtt: "译文字幕",
  subtitle_translated_srt: "译文字幕",
  subtitle_translated_qc: "译文字幕质检报告"
};

export function assetKindLabel(kind: string): string {
  return assetKindLabels[kind] ?? "媒体文件";
}

const languageLabels: Record<string, string> = {
  auto: "自动识别",
  zh: "中文",
  "zh-CN": "简体中文",
  "zh-TW": "繁体中文",
  en: "英文",
  ja: "日文",
  ko: "韩文"
};

export function languageLabel(language: string): string {
  return languageLabels[language] ?? "其他语言";
}

const errorCodeLabels: Record<string, string> = {
  model_request_failed: "模型服务请求失败",
  model_response_invalid: "模型响应格式无效",
  asr_request_failed: "语音识别请求失败",
  asr_upload_policy_failed: "语音识别上传凭证获取失败",
  asr_upload_failed: "语音识别音频上传失败",
  asr_submit_failed: "语音识别任务提交失败",
  asr_poll_failed: "语音识别结果查询失败",
  asr_timeout: "语音识别等待超时",
  asr_remote_failed: "远端语音识别失败",
  subtitle_translation_incomplete: "字幕翻译不完整",
  smart_segmentation_invalid: "智能分段结果无效"
};

export function errorCodeLabel(code: string): string {
  return errorCodeLabels[code] ?? "处理失败";
}

export function friendlyErrorMessage(message: string): string {
  return message
	.replace(
	  /(?:account_check_failed[:：]?\s*)?AcFun 创作中心返回 HTTP \d+/gi,
	  "无法确认 AcFun 登录状态，请重新同步 Cookie 后再校验。"
	)
	.replace(
	  /youtube api rejected request: quota exceeded for quota metric ['"]?search (?:queries|requests)['"]?[\s\S]*/gi,
	  "YouTube Data API 当日搜索配额已用完；已停止本批次，不会生成不完整任务。配额恢复后可重新执行。"
	)
	.replace(
	  /monitor request limit reached before video details could be loaded/gi,
	  "单次请求上限不足：至少需要 2 次请求才能读取视频详情"
	)
    .replace(/The read operation timed out/gi, "读取响应超时")
    .replace(/total request timeout exceeded \((\d+)s\)/gi, "请求超过 $1 秒总时限")
    .replace(/timed out/gi, "请求超时")
    .replace(/connection refused/gi, "服务拒绝连接")
    .replace(/no route to host/gi, "无法连接服务");
}

export const cookieStatusLabels: Record<string, string> = {
  ready: "可用",
  syncing: "同步中",
  error: "同步失败"
};
