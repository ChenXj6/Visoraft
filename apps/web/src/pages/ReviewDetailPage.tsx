import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import {
  api,
  ApiError,
  type ModerationChannelResult,
  type ReviewDetail,
  type SubtitleSegment
} from "../api";
import {
  LoadingBlock,
  PageHeader,
  PlatformChips,
  QueryError,
  StatusBadge
} from "../components";
import { formatDateTime, friendlyErrorMessage, languageLabel } from "../format";
import { Icon } from "../icons";
import { TransientNotice } from "../product-ui";

function errorMessage(error: unknown, fallback: string) {
  return error instanceof ApiError ? error.message : fallback;
}

const reviewActionLabels: Record<string, string> = {
  approve: "审核通过",
  request_changes: "退回修改",
  resubmit: "重新提交",
  reprocess_subtitles: "退回并重新处理字幕",
  abandon: "放弃任务",
  subtitle_edit: "字幕修订",
  automatic_approve: "自动审核通过",
  automatic_fallback: "自动审核转人工",
  automatic_reject: "自动审核未通过",
  automatic_manual_review: "自动审核转人工",
  automatic_block: "自动审核阻断",
  automatic_moderation_fallback: "内容审核转人工"
};

const subtitleSourceLabels: Record<string, string> = {
  youtube_manual: "YouTube 人工字幕",
  youtube_auto: "YouTube 自动字幕",
  embedded: "媒体内嵌字幕轨",
  asr: "语音识别产物",
  fixture: "本地测试产物",
  model: "模型翻译产物",
  edited: "人工修订"
};

function ModerationChannel({
  label,
  result
}: {
  label: string;
  result: ModerationChannelResult;
}) {
  return (
    <li>
      <div className="moderation-channel-head">
        <strong>{label}</strong>
        <span className={`risk-pill risk-${result.risk_level || "none"}`}>
          {result.status === "skipped"
            ? "未启用"
            : result.risk_level === "high"
              ? "高风险"
              : result.risk_level === "medium"
                ? "中风险"
                : result.risk_level === "low"
                  ? "低风险"
                  : "未发现风险"}
        </span>
      </div>
      <p>
        服务：{result.service || "未记录"}
        {result.request_ids.length > 0
          ? ` · 请求编号 ${result.request_ids.join("、")}`
          : ""}
      </p>
      {result.findings.length > 0 && (
        <ul className="moderation-findings">
          {result.findings.map((finding, index) => (
            <li key={`${finding.label}-${index}`}>
              <strong>{finding.label}</strong>
              <span>{finding.description || finding.location || "平台返回风险标签"}</span>
            </li>
          ))}
        </ul>
      )}
    </li>
  );
}

export default function ReviewDetailPage() {
  const { taskId = "" } = useParams();
  const queryClient = useQueryClient();
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [tags, setTags] = useState("");
  const [category, setCategory] = useState("");
  const [editReason, setEditReason] = useState("");
  const [actionReason, setActionReason] = useState("");
  const [deleteAssets, setDeleteAssets] = useState(false);
  const [notice, setNotice] = useState("");
  const [activeSubtitle, setActiveSubtitle] = useState(-1);
  const [subtitleEditing, setSubtitleEditing] = useState(false);
  const [subtitleReason, setSubtitleReason] = useState("");
  const [subtitleDraft, setSubtitleDraft] = useState<SubtitleSegment[]>([]);

  const review = useQuery({
    queryKey: ["review", taskId],
    queryFn: () => api.review(taskId),
    enabled: Boolean(taskId),
    refetchInterval: 5_000
  });

  useEffect(() => {
    if (!review.data) return;
    setTitle(review.data.task.title);
    setDescription(review.data.task.description);
    setTags(review.data.task.tags.join(", "));
    setCategory(review.data.task.category);
  }, [review.data]);

  const refresh = async (detail: ReviewDetail) => {
    queryClient.setQueryData(["review", taskId], detail);
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["reviews"] }),
      queryClient.invalidateQueries({ queryKey: ["tasks"] }),
      queryClient.invalidateQueries({ queryKey: ["task", taskId] }),
      queryClient.invalidateQueries({ queryKey: ["dashboard"] })
    ]);
  };

  const metadata = useMutation({
    mutationFn: () =>
      api.updateReviewMetadata(taskId, {
        title: title.trim(),
        description: description.trim(),
        tags: tags.split(/[,，\n]/).map((value) => value.trim()).filter(Boolean),
        category: category.trim(),
        reason: editReason.trim()
      }),
    onSuccess: async (detail) => {
      setEditReason("");
      setNotice("元数据新版本已保存，旧版本继续保留在审计记录中。");
      await refresh(detail);
    },
    onError: (error) => setNotice(errorMessage(error, "元数据保存失败"))
  });

  const action = useMutation({
    mutationFn: (kind: "approve" | "request_changes" | "resubmit" | "reprocess_subtitles" | "abandon") =>
      api.reviewAction(taskId, kind, {
        reason: actionReason.trim(),
        delete_assets: kind === "abandon" ? deleteAssets : false
      }),
    onSuccess: async (detail, kind) => {
      setActionReason("");
      setDeleteAssets(false);
      setNotice(
        kind === "approve"
          ? "审核已批准，任务进入待发布。"
          : kind === "request_changes"
            ? "任务已退回修改。"
            : kind === "reprocess_subtitles"
              ? "任务已退回字幕处理阶段，可在任务详情查看重跑进度。"
            : kind === "resubmit"
              ? "任务已重新提交；若启用了字幕烧录，将先重新转码，再回到审核。"
              : "任务已放弃。"
      );
      await refresh(detail);
    },
    onError: (error) => setNotice(errorMessage(error, "审核操作失败"))
  });

  const latestRun = review.data?.runs[0];
  const latestModeration = review.data?.moderation_runs[0];
  const subtitle = review.data?.subtitles[activeSubtitle];
  const qcScore = useMemo(() => {
    const score = subtitle?.qc_report.score;
    return typeof score === "number" ? score : undefined;
  }, [subtitle]);

  useEffect(() => {
    setActiveSubtitle(-1);
  }, [taskId]);

  useEffect(() => {
    if (!review.data || activeSubtitle >= 0) return;
    const translatedIndex = review.data.subtitles.findIndex(
      (document) => document.kind === "translated"
    );
    setActiveSubtitle(translatedIndex >= 0 ? translatedIndex : 0);
  }, [activeSubtitle, review.data]);

  useEffect(() => {
    setSubtitleEditing(false);
    setSubtitleReason("");
    setSubtitleDraft(
      subtitle?.segments.map((segment) => ({ ...segment })) ?? []
    );
  }, [subtitle?.id]);

  const subtitleUpdate = useMutation({
    mutationFn: () => {
      if (!subtitle) {
        throw new Error("没有可修订的字幕文档");
      }
      return api.updateReviewSubtitle(taskId, subtitle.id, {
        expected_version: subtitle.version,
        segments: subtitleDraft,
        reason: subtitleReason.trim()
      });
    },
    onSuccess: async (detail) => {
      const nextIndex = detail.subtitles.findIndex(
        (document) =>
          document.kind === subtitle?.kind &&
          document.version === (subtitle?.version ?? 0) + 1
      );
      if (nextIndex >= 0) setActiveSubtitle(nextIndex);
      setSubtitleEditing(false);
      setSubtitleReason("");
      setNotice("字幕新版本已保存并重新计算质检；旧版本继续保留。");
      await refresh(detail);
    },
    onError: (error) => setNotice(errorMessage(error, "字幕版本保存失败"))
  });

  if (review.isPending) return <LoadingBlock label="正在打开审核台" />;
  if (review.isError || !review.data) {
    return (
      <QueryError
        title="无法打开审核任务"
        message={errorMessage(review.error, "审核详情不存在或服务不可用")}
        retry={() => void review.refetch()}
      />
    );
  }

  const task = review.data.task;
  const subtitleDecision = task.steps.find(
    (step) => step.kind === "subtitles"
  )?.detail.decision;
  const editable = task.status === "awaiting_manual_review";
  const changesRequested = task.review_status === "changes_requested";
  const sourceAsset = task.assets.find(
    (asset) => asset.kind === "source" && asset.status === "available"
  );
  const subtitleTracks = task.assets.filter(
    (asset) => asset.content_type.startsWith("text/vtt") && asset.status === "available"
  );

  return (
    <>
      <PageHeader
        title={task.title || task.original_title || "未命名审核任务"}
        description="逐项核对媒体、元数据、字幕和自动规则。所有修改与判定都会追加记录。"
        actions={
          <div className="review-heading-actions">
            <PlatformChips platforms={task.target_platforms} />
            <StatusBadge status={task.status} />
            <Link className="button button-secondary" to="/reviews">
              返回队列
            </Link>
          </div>
        }
      />

      {notice && (
        <TransientNotice
          tone={/失败|错误/.test(notice) ? "error" : "success"}
          onDismiss={() => setNotice("")}
        >
          {notice}
        </TransientNotice>
      )}
      {task.status === "ready_to_publish" && (
        <section className="review-next-step" aria-labelledby="review-next-title">
          <div>
            <span>审核后的下一步</span>
            <h2 id="review-next-title">确认投稿设置并加入发布队列</h2>
            <p>这条数据没有消失，已从待审核队列转入“等待发布”。可继续查看任务，或直接进入投稿准备。</p>
          </div>
          <div className="review-next-actions">
            <Link className="button button-primary" to={`/publishing/${task.id}`}>
              进入投稿准备
            </Link>
            <Link className="button button-secondary" to={`/tasks/${task.id}`}>
              查看任务详情
            </Link>
          </div>
        </section>
      )}
      {task.status === "processing" && (
        <section className="review-next-step" aria-labelledby="review-processing-title">
          <div>
            <span>当前处理状态</span>
            <h2 id="review-processing-title">字幕与后续媒体正在重新生成</h2>
            <p>完成后任务会生成新的字幕版本、重新转码，并再次回到人工审核队列。</p>
          </div>
          <div className="review-next-actions">
            <Link className="button button-primary" to={`/tasks/${task.id}`}>
              查看重跑进度
            </Link>
          </div>
        </section>
      )}

      <div className="review-detail-grid">
        <div className="review-canvas">
          <section className="work-panel review-media-panel">
            <header className="section-heading">
              <span className="sequence-mark"><Icon name="media" /></span>
              <div>
                <h2>媒体与产物</h2>
            <p>查看源媒体信息、任务封面和可用文件；预览链接仅在当前查看期间有效。</p>
              </div>
            </header>
            <div className="media-stage">
              {sourceAsset?.content_type.startsWith("video/") ? (
                <video
                  controls
                  preload="metadata"
                  poster={task.thumbnail_url || undefined}
                  src={api.assetContentURL(task.id, sourceAsset.id)}
                >
                  {subtitleTracks.map((asset) => (
                    <track
                      key={asset.id}
                      kind="subtitles"
                      label={
                        asset.kind.includes("translated") ? "翻译字幕" : "原始字幕"
                      }
                      srcLang={
                        asset.kind.includes("translated")
                          ? review.data.subtitles.find(
                              (document) => document.kind === "translated"
                            )?.language ?? "zh"
                          : review.data.subtitles.find(
                              (document) => document.kind === "original"
                            )?.language ?? "und"
                      }
                      src={api.assetContentURL(task.id, asset.id)}
                      default={asset.kind.includes("translated")}
                    />
                  ))}
                  当前浏览器不支持 HTML5 视频播放。
                </video>
              ) : sourceAsset?.content_type.startsWith("audio/") ? (
                <div className="media-audio-preview">
                  {task.thumbnail_url ? (
                    <img src={task.thumbnail_url} alt="" />
                  ) : (
                    <span aria-hidden="true"><Icon name="media" /></span>
                  )}
                  <audio
                    controls
                    preload="metadata"
                    src={api.assetContentURL(task.id, sourceAsset.id)}
                  >
                    当前浏览器不支持 HTML5 音频播放。
                  </audio>
                </div>
              ) : task.thumbnail_url ? (
                <img src={task.thumbnail_url} alt="任务封面预览" />
              ) : (
                <div className="media-placeholder">暂无封面预览</div>
              )}
              <dl>
                <div>
                  <dt>源媒体</dt>
                  <dd>{sourceAsset?.original_name ?? "缺失"}</dd>
                </div>
                <div>
                  <dt>时长</dt>
                  <dd>{task.duration_seconds ? `${task.duration_seconds} 秒` : "未知"}</dd>
                </div>
                <div>
                  <dt>字幕文件</dt>
                  <dd>
                    {task.assets.filter((asset) => asset.kind.startsWith("subtitle_")).length} 个
                    {subtitleTracks.length > 0 && (
                      <>
                        {" · "}
                        {subtitleTracks.map((asset, index) => (
                          <span key={asset.id}>
                            {index > 0 && " / "}
                            <a
                              href={api.assetContentURL(task.id, asset.id)}
                              target="_blank"
                              rel="noreferrer"
                            >
                              {asset.kind.includes("translated") ? "译文" : "原文"} VTT
                            </a>
                          </span>
                        ))}
                      </>
                    )}
                  </dd>
                </div>
              </dl>
            </div>
          </section>

          <section className="work-panel review-edit-panel">
            <header className="section-heading">
              <span className="sequence-mark"><Icon name="sliders" /></span>
              <div>
                <h2>最终元数据</h2>
                <p>保存会创建新版本，不覆盖之前审核过的内容。</p>
              </div>
            </header>
            <div className="settings-form-grid">
              <label className="field field-wide">
                <span>标题</span>
                <input
                  value={title}
                  disabled={!editable}
                  onChange={(event) => setTitle(event.target.value)}
                />
              </label>
              <label className="field field-wide">
                <span>简介</span>
                <textarea
                  rows={8}
                  value={description}
                  disabled={!editable}
                  onChange={(event) => setDescription(event.target.value)}
                />
              </label>
              <label className="field">
                <span>标签（逗号分隔）</span>
                <input
                  value={tags}
                  disabled={!editable}
                  onChange={(event) => setTags(event.target.value)}
                />
              </label>
              <label className="field">
                <span>分区</span>
                <input
                  value={category}
                  disabled={!editable}
                  onChange={(event) => setCategory(event.target.value)}
                />
              </label>
              <label className="field field-wide">
                <span>本次修改原因</span>
                <input
                  value={editReason}
                  disabled={!editable}
                  onChange={(event) => setEditReason(event.target.value)}
                  placeholder="例如：修正专有名词与标签"
                />
              </label>
            </div>
            <button
              className="button button-secondary"
              type="button"
              disabled={!editable || metadata.isPending}
              onClick={() => metadata.mutate()}
            >
              {metadata.isPending ? "正在保存版本…" : "保存元数据版本"}
            </button>
          </section>

          <section className="work-panel review-subtitle-panel">
            <header className="section-heading">
              <span className="sequence-mark"><Icon name="subtitles" /></span>
              <div>
                <h2>字幕与质检</h2>
                <p>原文、译文、时间轴和质检报告均来自实际处理服务。</p>
              </div>
            </header>
            {review.data.subtitles.length === 0 ? (
              subtitleDecision?.disposition === "existing_hardcoded_chinese" ? (
                <div className="hardcoded-subtitle-review" role="note">
                  <strong>视频画面已有中文字幕</strong>
                  <p>
                    {subtitleDecision.detection.reason}
                    系统已跳过 ASR、字幕翻译和重复烧录，原视频画面保持不变。
                  </p>
                  <dl>
                    <div>
                      <dt>识别置信度</dt>
                      <dd>{subtitleDecision.detection.confidence_percent}%</dd>
                    </div>
                    <div>
                      <dt>画面抽检</dt>
                      <dd>
                        {subtitleDecision.detection.sample_count} 帧 ·
                        {subtitleDecision.detection.stable_pair_count} 组稳定字幕
                      </dd>
                    </div>
                  </dl>
                </div>
              ) : (
                <div className="media-placeholder">此任务没有字幕文档</div>
              )
            ) : (
              <>
                <div className="subtitle-tabs" role="tablist" aria-label="字幕版本">
                  {review.data.subtitles.map((document, index) => (
                    <button
                      type="button"
                      role="tab"
                      aria-selected={activeSubtitle === index}
                      className={activeSubtitle === index ? "is-active" : ""}
                      onClick={() => setActiveSubtitle(index)}
                      key={document.id}
                    >
                      {document.kind === "translated" ? "译文" : "原文"} · {languageLabel(document.language)} · v
                      {document.version}
                    </button>
                  ))}
                </div>
                {editable && subtitle && (
                  <div className="subtitle-edit-toolbar">
                    <p>
                      当前为 {subtitleSourceLabels[subtitle.source] ?? "处理服务产物"}；
                      保存修订会新增 v{subtitle.version + 1}，不会覆盖 v{subtitle.version}。
                    </p>
                    <button
                      className="button button-secondary"
                      type="button"
                      disabled={subtitleUpdate.isPending}
                      onClick={() => {
                        setSubtitleEditing((current) => !current);
                        setSubtitleDraft(
                          subtitle.segments.map((segment) => ({ ...segment }))
                        );
                        setSubtitleReason("");
                      }}
                    >
                      {subtitleEditing ? "取消字幕修订" : "修订当前字幕"}
                    </button>
                  </div>
                )}
                <div className="subtitle-qc-strip">
                  <strong>{qcScore === undefined ? "--" : qcScore.toFixed(1)}</strong>
                  <span>质检分数</span>
                  <p>
                    {subtitle?.qc_report.passed === false ? "未达到通过阈值" : "时间轴与可读性检查完成"}
                    </p>
                  </div>
                {subtitleEditing && subtitle ? (
                  <div className="subtitle-editor">
                    <ol className="subtitle-editor-list">
                      {subtitleDraft.map((segment, index) => (
                        <li key={`${subtitle.id}-edit-${segment.index}`}>
                          <div className="subtitle-time-fields">
                            <label>
                              <span>第 {index + 1} 条开始时间</span>
                              <input
                                type="number"
                                min="0"
                                step="0.001"
                                value={segment.start}
                                onChange={(event) =>
                                  setSubtitleDraft((current) =>
                                    current.map((item, itemIndex) =>
                                      itemIndex === index
                                        ? { ...item, start: Number(event.target.value) }
                                        : item
                                    )
                                  )
                                }
                              />
                            </label>
                            <label>
                              <span>第 {index + 1} 条结束时间</span>
                              <input
                                type="number"
                                min="0.001"
                                step="0.001"
                                value={segment.end}
                                onChange={(event) =>
                                  setSubtitleDraft((current) =>
                                    current.map((item, itemIndex) =>
                                      itemIndex === index
                                        ? { ...item, end: Number(event.target.value) }
                                        : item
                                    )
                                  )
                                }
                              />
                            </label>
                          </div>
                          <label>
                            <span>第 {index + 1} 条字幕文本</span>
                            <textarea
                              rows={3}
                              value={segment.text}
                              onChange={(event) =>
                                setSubtitleDraft((current) =>
                                  current.map((item, itemIndex) =>
                                    itemIndex === index
                                      ? { ...item, text: event.target.value }
                                      : item
                                  )
                                )
                              }
                            />
                          </label>
                        </li>
                      ))}
                    </ol>
                    <label className="field">
                      <span>字幕修改原因</span>
                      <input
                        value={subtitleReason}
                        onChange={(event) => setSubtitleReason(event.target.value)}
                        placeholder="例如：修正术语并微调第二条时间轴"
                      />
                    </label>
                    <button
                      className="button button-primary"
                      type="button"
                      disabled={
                        subtitleUpdate.isPending ||
                        subtitleReason.trim() === "" ||
                        subtitleDraft.length === 0
                      }
                      onClick={() => subtitleUpdate.mutate()}
                    >
                      {subtitleUpdate.isPending ? "正在保存字幕…" : "保存字幕新版本"}
                    </button>
                  </div>
                ) : (
                  <ol className="subtitle-timeline">
                    {subtitle?.segments.map((segment) => (
                      <li key={`${subtitle.id}-${segment.index}`}>
                        <time>
                          <Icon name="timeRange" />
                          <span>{segment.start.toFixed(2)}</span>
                          <span aria-hidden="true">–</span>
                          <span>{segment.end.toFixed(2)}</span>
                        </time>
                        <p>{segment.text}</p>
                      </li>
                    ))}
                  </ol>
                )}
              </>
            )}
          </section>
        </div>

        <aside className="review-inspector">
          <section className="work-panel rule-inspector">
            <header>
              <p className="eyebrow">审核规则</p>
              <h2>{latestRun?.mode === "automatic" ? "自动审核结果" : "审核检查表"}</h2>
              <p>{latestRun?.summary ?? "等待审核运行记录"}</p>
            </header>
            <ul>
              {latestRun?.rule_results.map((rule) => (
                <li className={rule.passed ? "rule-pass" : "rule-fail"} key={rule.key}>
                  <span aria-hidden="true">{rule.passed ? "✓" : "×"}</span>
                  <div>
                    <strong>{rule.label}</strong>
                    <small>{rule.message}</small>
                  </div>
                </li>
              ))}
            </ul>
          </section>

          <section className="work-panel moderation-inspector">
            <header>
              <p className="eyebrow">内容安全依据</p>
              <h2>自动审核使用了什么</h2>
              <p>
                自动审核先执行下方确定性规则；启用内容安全时，再读取冻结在本任务中的服务配置和结果。
              </p>
            </header>
            {latestModeration ? (
              <>
                <dl className="moderation-summary">
                  <div>
                    <dt>提供方</dt>
                    <dd>
                      {latestModeration.provider === "fixture"
                    ? "本地演示检查"
                        : latestModeration.provider}
                    </dd>
                  </div>
                  <div>
                    <dt>最终风险</dt>
                    <dd>{latestModeration.risk_level || "none"}</dd>
                  </div>
                  <div>
                    <dt>最终决策</dt>
                    <dd>
                      {latestModeration.decision === "pass"
                        ? "通过"
                        : latestModeration.decision === "manual_review"
                          ? "转人工复核"
                          : latestModeration.decision === "block"
                            ? "阻断投稿"
                            : latestModeration.decision || "等待结果"}
                    </dd>
                  </div>
                  <div>
                    <dt>完成时间</dt>
                    <dd>
                      {latestModeration.completed_at
                        ? formatDateTime(latestModeration.completed_at)
                        : "尚未完成"}
                    </dd>
                  </div>
                </dl>
                <ul className="moderation-channels">
                  <ModerationChannel label="文本" result={latestModeration.text_result} />
                  <ModerationChannel label="图片" result={latestModeration.image_result} />
                  <ModerationChannel label="视频" result={latestModeration.video_result} />
                </ul>
                {latestModeration.error_message && (
                  <p className="moderation-error" role="alert">
                    {latestModeration.error_code
                      ? `${latestModeration.error_code}：`
                      : ""}
                    {friendlyErrorMessage(latestModeration.error_message)}
                  </p>
                )}
              </>
            ) : (
              <p className="moderation-empty">
                本任务未产生内容安全运行记录；自动审核仍按媒体、标题、简介长度、时长和字幕质检规则执行。
              </p>
            )}
          </section>

          <section className="work-panel review-action-panel">
            <header>
              <p className="eyebrow">审核判定</p>
              <h2>审核判定</h2>
            </header>
            <label className="field">
              <span>意见 / 原因</span>
              <textarea
                rows={5}
                value={actionReason}
                disabled={!editable}
                onChange={(event) => setActionReason(event.target.value)}
                placeholder="退回、重新提交或放弃时必须填写"
              />
            </label>
            <div className="review-action-stack">
              {changesRequested ? (
                <button
                  className="button button-primary"
                  type="button"
                  disabled={action.isPending}
                  onClick={() => action.mutate("resubmit")}
                >
                  修改完成，重新提交
                </button>
              ) : (
                <>
                  <button
                    className="button button-primary"
                    type="button"
                    disabled={!editable || action.isPending}
                    onClick={() => action.mutate("approve")}
                  >
                    批准并进入待发布
                  </button>
                  <button
                    className="button button-secondary"
                    type="button"
                    disabled={!editable || action.isPending}
                    onClick={() => action.mutate("request_changes")}
                  >
                    退回修改
                  </button>
                  <button
                    className="button button-secondary"
                    type="button"
                    disabled={!editable || action.isPending || !actionReason.trim()}
                    onClick={() => action.mutate("reprocess_subtitles")}
                  >
                    退回并重新处理字幕
                  </button>
                </>
              )}
              <label className="cleanup-check">
                <input
                  type="checkbox"
                  checked={deleteAssets}
                  disabled={!editable}
                  onChange={(event) => setDeleteAssets(event.target.checked)}
                />
                放弃时同时清理媒体文件
              </label>
              <button
                className="button button-danger"
                type="button"
                disabled={!editable || action.isPending}
                onClick={() => action.mutate("abandon")}
              >
                放弃任务
              </button>
            </div>
          </section>

          <section className="work-panel audit-mini">
            <p className="eyebrow">审计记录</p>
            <h2>操作记录</h2>
            <ol>
              {review.data.actions.map((item) => (
                <li key={item.id}>
                  <strong>{reviewActionLabels[item.action] ?? "审核操作"}</strong>
                  <span>{item.reason || "无附加意见"}</span>
                  <time>{formatDateTime(item.created_at)}</time>
                </li>
              ))}
            </ol>
          </section>
        </aside>
      </div>
    </>
  );
}
