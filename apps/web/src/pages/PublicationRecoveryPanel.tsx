import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import {
  api,
  ApiError,
  type PlatformPublication,
  type PublishingDetail,
  type PublishingResolutionInput
} from "../api";

type Resolution = PublishingResolutionInput["resolution"] | "";
type FieldErrors = Record<string, string>;

function messageOf(error: unknown) {
  return error instanceof ApiError ? error.message : "暂时无法保存核验结果";
}

function validate(
  resolution: Resolution,
  remoteSubmissionId: string,
  remoteUrl: string,
  note: string
) {
  const errors: FieldErrors = {};
  if (!resolution) errors.resolution = "请选择已经核验的远端结果";
  if (note.trim().length < 4) errors.note = "请填写至少 4 个字符的核验说明";
  if (note.trim().length > 500) errors.note = "核验说明不能超过 500 个字符";
  if (resolution === "remote_published" && !remoteSubmissionId.trim()) {
    errors.remote_submission_id = "请填写平台稿件编号";
  }
  if (remoteUrl.trim()) {
    try {
      const parsed = new URL(remoteUrl.trim());
      if (!["http:", "https:"].includes(parsed.protocol)) throw new Error();
    } catch {
      errors.remote_url = "请输入完整的 http 或 https 稿件地址";
    }
  }
  return errors;
}

function RecoveryIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M12 3.5 20 7v5.2c0 4.1-2.6 7.2-8 8.3-5.4-1.1-8-4.2-8-8.3V7l8-3.5Z" />
      <path d="M12 8v5.2M12 16.5h.01" />
    </svg>
  );
}

export function PublicationRecoveryPanel({
  taskId,
  publication,
  onResolved,
  onRefresh
}: {
  taskId: string;
  publication: PlatformPublication;
  onResolved: (detail: PublishingDetail, message: string) => Promise<void>;
  onRefresh: () => void;
}) {
  const [resolution, setResolution] = useState<Resolution>("");
  const [remoteSubmissionId, setRemoteSubmissionId] = useState("");
  const [remoteUrl, setRemoteUrl] = useState("");
  const [note, setNote] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});

  const mutation = useMutation({
    mutationFn: (input: PublishingResolutionInput) =>
      api.resolvePlatformPublishing(taskId, publication.platform, input),
    onSuccess: (detail, input) =>
      onResolved(
        detail,
        input.resolution === "remote_published"
          ? "已按平台稿件编号确认发布结果。"
          : "已确认远端未生成稿件，并安全重新加入发布队列。"
      ),
    onError: (error) => {
      if (error instanceof ApiError && error.fields) {
        setFieldErrors(error.fields);
      }
    }
  });

  const choose = (value: Resolution) => {
    setResolution(value);
    setConfirmed(false);
    setFieldErrors({});
    mutation.reset();
    if (value === "remote_not_created") {
      setRemoteSubmissionId("");
      setRemoteUrl("");
    }
  };

  const submit = () => {
    const errors = validate(resolution, remoteSubmissionId, remoteUrl, note);
    setFieldErrors(errors);
    if (Object.keys(errors).length > 0 || !resolution || !confirmed) return;
    mutation.mutate({
      expected_version: publication.version,
      resolution,
      remote_submission_id:
        resolution === "remote_published" ? remoteSubmissionId.trim() : "",
      remote_url: resolution === "remote_published" ? remoteUrl.trim() : "",
      note: note.trim()
    });
  };

  const fieldError = (name: string) =>
    fieldErrors[name] ? (
      <small className="field-error" id={`${publication.id}-${name}-error`}>
        {fieldErrors[name]}
      </small>
    ) : null;

  return (
    <section className="publication-recovery" aria-labelledby={`${publication.id}-recovery-title`}>
      <header>
        <span className="publication-recovery-icon">
          <RecoveryIcon />
        </span>
        <div>
          <p className="eyebrow">需要人工核验</p>
          <h3 id={`${publication.id}-recovery-title`}>远端投稿结果不确定</h3>
          <p>先到平台创作中心核对上传记录。未核验前不要直接重试，以免重复投稿。</p>
        </div>
      </header>

      <fieldset className="publication-resolution-options">
        <legend>核验结果</legend>
        <label className={resolution === "remote_published" ? "is-selected" : ""}>
          <input
            type="radio"
            name={`${publication.id}-resolution`}
            checked={resolution === "remote_published"}
            disabled={mutation.isPending}
            onChange={() => choose("remote_published")}
          />
          <span>
            <strong>已找到平台稿件</strong>
            <small>登记稿件编号，本平台流程按已发布结束。</small>
          </span>
        </label>
        <label className={resolution === "remote_not_created" ? "is-selected" : ""}>
          <input
            type="radio"
            name={`${publication.id}-resolution`}
            checked={resolution === "remote_not_created"}
            disabled={mutation.isPending}
            onChange={() => choose("remote_not_created")}
          />
          <span>
            <strong>确认没有生成稿件</strong>
            <small>清除不确定状态，并重新加入可靠发布队列。</small>
          </span>
        </label>
        {fieldError("resolution")}
      </fieldset>

      {resolution === "remote_published" && (
        <div className="publication-recovery-fields">
          <label className="field">
            <span>平台稿件编号</span>
            <input
              value={remoteSubmissionId}
              disabled={mutation.isPending}
              aria-invalid={Boolean(fieldErrors.remote_submission_id)}
              aria-describedby={
                fieldErrors.remote_submission_id
                  ? `${publication.id}-remote_submission_id-error`
                  : undefined
              }
              onChange={(event) => setRemoteSubmissionId(event.target.value)}
            />
            {fieldError("remote_submission_id")}
          </label>
          <label className="field">
            <span>稿件地址（可选）</span>
            <input
              type="url"
              placeholder="https://"
              value={remoteUrl}
              disabled={mutation.isPending}
              aria-invalid={Boolean(fieldErrors.remote_url)}
              aria-describedby={
                fieldErrors.remote_url ? `${publication.id}-remote_url-error` : undefined
              }
              onChange={(event) => setRemoteUrl(event.target.value)}
            />
            {fieldError("remote_url")}
          </label>
        </div>
      )}

      {resolution && (
        <>
          <label className="field publication-recovery-note">
            <span>核验说明</span>
            <textarea
              rows={3}
              maxLength={500}
              placeholder="记录核验位置、时间和判断依据"
              value={note}
              disabled={mutation.isPending}
              aria-invalid={Boolean(fieldErrors.note)}
              aria-describedby={fieldErrors.note ? `${publication.id}-note-error` : undefined}
              onChange={(event) => setNote(event.target.value)}
            />
            <small>{note.length}/500</small>
            {fieldError("note")}
          </label>
          <label className="publication-recovery-confirm">
            <input
              type="checkbox"
              checked={confirmed}
              disabled={mutation.isPending}
              onChange={(event) => setConfirmed(event.target.checked)}
            />
            <span>
              {resolution === "remote_published"
                ? "我已在平台创作中心打开并核对了这条稿件"
                : "我已在平台创作中心确认没有对应稿件，可以再次投稿"}
            </span>
          </label>
        </>
      )}

      {mutation.isError && (
        <div className="publication-recovery-error" role="alert">
          <strong>{messageOf(mutation.error)}</strong>
          <span>状态可能已变化，请刷新后再核验。</span>
        </div>
      )}

      <div className="publication-recovery-actions">
        <button
          className="button button-primary"
          type="button"
          disabled={!resolution || !confirmed || mutation.isPending}
          onClick={submit}
        >
          {mutation.isPending
            ? "正在保存核验结果…"
            : resolution === "remote_published"
              ? "确认已发布"
              : "确认未创建并安全重投"}
        </button>
        <button
          className="button button-secondary"
          type="button"
          disabled={mutation.isPending}
          onClick={onRefresh}
        >
          刷新平台状态
        </button>
      </div>
    </section>
  );
}
