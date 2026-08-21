import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, ApiError, type StatementVersion, type Task } from "../api";
import { ConfirmDialog, EmptyState, LoadingBlock, PlatformChips, QueryError, TaskTitle } from "../components";
import { assetKindLabel, formatBytes, formatDateTime, formatDuration, formatRelativeTime, shortID, statusLabel, stepLabel } from "../format";
import { Icon } from "../icons";
import { TransientNotice } from "../product-ui";

type ReviewTab = "intro" | "subtitles" | "media" | "history";

const reviewTabs: { key: ReviewTab; label: string }[] = [
  { key: "intro", label: "简介与标签" },
  { key: "subtitles", label: "字幕全文" },
  { key: "media", label: "封面与媒体" },
  { key: "history", label: "审核记录" },
];

function preferredVideo(task: Task) {
  return task.assets.find((asset) => asset.status === "available" && asset.kind === "transcoded")
    ?? task.assets.find((asset) => asset.status === "available" && asset.kind === "source")
    ?? task.assets.find((asset) => asset.status === "available" && asset.content_type.startsWith("video/"));
}

export default function ReviewQueuePage() {
  const queryClient = useQueryClient();
  const videoRef = useRef<HTMLVideoElement>(null);
  const reviews = useQuery({ queryKey: ["reviews"], queryFn: api.reviews, refetchInterval: 5_000 });
  const [activeID, setActiveID] = useState("");
  const [activeTab, setActiveTab] = useState<ReviewTab>("intro");
  const [mediaPlaying, setMediaPlaying] = useState(false);
  const [rejectOpen, setRejectOpen] = useState(false);
  const [rejectReason, setRejectReason] = useState("");
  const [notice, setNotice] = useState("");
  const items = reviews.data?.items ?? [];
  const activeTask = items.find((task) => task.id === activeID) ?? items[0];
  const activeIndex = Math.max(0, items.findIndex((task) => task.id === activeTask?.id));
  const activeReview = useQuery({
    queryKey: ["review", activeTask?.id],
    queryFn: () => api.review(activeTask!.id),
    enabled: Boolean(activeTask?.id),
    staleTime: 3_000,
  });
  const mediaAsset = activeTask ? preferredVideo(activeTask) : undefined;
  const mediaURL = activeTask && mediaAsset ? api.assetContentURL(activeTask.id, mediaAsset.id) : "";
  const subtitleText = useMemo(() => {
    const document = activeReview.data?.subtitles.find((item) => item.kind.includes("translated")) ?? activeReview.data?.subtitles[0];
    return document?.segments.map((segment) => segment.text.trim()).filter(Boolean).join("\n") ?? "";
  }, [activeReview.data?.subtitles]);

  const syncReviewCaches = async (taskID: string, detail?: Awaited<ReturnType<typeof api.review>>) => {
    if (detail) {
      queryClient.setQueryData(["review", taskID], detail);
      queryClient.setQueryData<{ items: Task[] }>(["reviews"], (current) => current ? {
        ...current,
        items: current.items.map((item) => item.id === taskID ? detail.task : item),
      } : current);
    }
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["reviews"] }),
      queryClient.invalidateQueries({ queryKey: ["review", taskID] }),
      queryClient.invalidateQueries({ queryKey: ["tasks"] }),
      queryClient.invalidateQueries({ queryKey: ["task", taskID] }),
      queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
      queryClient.invalidateQueries({ queryKey: ["publishing-queue"] }),
      queryClient.invalidateQueries({ queryKey: ["publishing", taskID] }),
    ]);
  };

  const action = useMutation({
    mutationFn: ({ taskID, kind, reason }: { taskID: string; kind: "approve" | "request_changes"; reason: string }) =>
      api.reviewAction(taskID, kind, { reason, delete_assets: false }),
    onSuccess: async (detail, variables) => {
      setRejectOpen(false);
      setRejectReason("");
      setNotice(variables.kind === "approve" ? "审核已通过，任务已进入待发布阶段。" : "任务已驳回修改，可在任务详情中查看处理状态。");
      await syncReviewCaches(variables.taskID, detail);
    },
    onError: (error) => setNotice(error instanceof ApiError ? error.message : "审核操作失败，请稍后重试。"),
  });

  const statement = useMutation({
    mutationFn: ({ task, version }: { task: Task; version: StatementVersion }) => api.updateReviewMetadata(task.id, {
      title: task.title || task.original_title,
      description: task.description,
      tags: task.tags,
      category: task.category,
      repost_statement_version: version,
      reason: "审核台切换转载声明版本",
    }),
    onSuccess: async (detail) => {
      setNotice("转载声明版本已保存，并已同步到审核详情。");
      await syncReviewCaches(detail.task.id, detail);
    },
    onError: (error) => setNotice(error instanceof ApiError ? error.message : "转载声明保存失败"),
  });

  useEffect(() => {
    const firstTask = items[0];
    if (firstTask && !items.some((task) => task.id === activeID)) setActiveID(firstTask.id);
  }, [activeID, items]);

  useEffect(() => {
    setActiveTab("intro");
    setMediaPlaying(false);
    videoRef.current?.pause();
  }, [activeTask?.id]);

  return (
    <div className="review-master-page">
      <h1 className="sr-only">媒体审核</h1>
      {notice ? <TransientNotice tone={/失败|错误|不可用/.test(notice) ? "error" : "success"} onDismiss={() => setNotice("")}>{notice}</TransientNotice> : null}
      {reviews.isPending && <LoadingBlock label="正在读取审核队列" />}
      {reviews.isError && <QueryError title="审核队列暂时不可用" message={reviews.error instanceof ApiError ? reviews.error.message : "暂时无法连接审核服务"} retry={() => void reviews.refetch()} />}
      {reviews.data?.items.length === 0 && <EmptyState title="当前没有待人工审核任务" description="手动审核任务或自动审核失败转人工的任务会出现在这里。" action={<Link className="button button-secondary" to="/tasks">查看全部任务</Link>} />}

      {activeTask ? (
        <>
          <div className="prototype-review-batch" aria-label="审核进度">
            <span className="prototype-review-batch-copy">本批次 {activeIndex + 1} / {items.length} 已审</span>
            <span className="prototype-review-batch-progress" aria-hidden="true"><span style={{ width: `${((activeIndex + 1) / items.length) * 100}%` }} /></span>
          </div>
          <div className="prototype-review-workbench">
            <aside className="work-panel prototype-review-queue">
              <header><strong>待审队列</strong><span>{items.length}</span></header>
              <div>
                {items.map((task) => (
                  <button type="button" className={task.id === activeTask.id ? "is-active" : ""} onClick={() => setActiveID(task.id)} key={task.id}>
                    {task.thumbnail_url ? <img src={task.thumbnail_url} alt="" /> : <span><Icon name="media" /></span>}
                    <span><strong><TaskTitle task={task} /></strong><small>#{shortID(task.id)} · {formatRelativeTime(task.updated_at)}</small></span>
                  </button>
                ))}
              </div>
              <footer>共 {items.length} 条等待人工判定</footer>
            </aside>

            <section className="work-panel prototype-review-focus">
              <div className={`prototype-review-media ${mediaPlaying ? "is-playing" : ""}`} style={!mediaURL && activeTask.thumbnail_url ? { backgroundImage: `url(${activeTask.thumbnail_url})` } : undefined}>
                {mediaURL ? <video ref={videoRef} src={mediaURL} poster={activeTask.thumbnail_url || undefined} controls={mediaPlaying} playsInline preload="metadata" onPlay={() => setMediaPlaying(true)} onPause={() => setMediaPlaying(false)} onEnded={() => setMediaPlaying(false)} /> : null}
                {mediaURL && !mediaPlaying ? <button className="prototype-review-play" type="button" aria-label="播放审核视频" onClick={() => void videoRef.current?.play()}><Icon name="play" /></button> : null}
                <span className="prototype-review-media-status">{activeTask.assets.some((asset) => asset.kind.startsWith("subtitle_")) ? "中文字幕已就绪" : "字幕待处理"}</span>
                {!mediaPlaying ? <div className="prototype-review-media-copy"><strong><TaskTitle task={activeTask} /></strong><small>{formatDuration(activeTask.duration_seconds)} · {activeTask.assets.some((asset) => asset.kind.startsWith("subtitle_")) ? "已有字幕产物" : "暂无字幕产物"}</small></div> : null}
              </div>
              <nav className="prototype-review-tabs" aria-label="审核信息" role="tablist">
                {reviewTabs.map((tab) => <button key={tab.key} type="button" role="tab" aria-selected={activeTab === tab.key} className={activeTab === tab.key ? "is-active" : ""} onClick={() => setActiveTab(tab.key)}>{tab.label}</button>)}
              </nav>
              <div className="prototype-review-content" role="tabpanel">
                {activeTab === "intro" ? <>
                  <div><label>标题</label><p>{activeTask.title || activeTask.original_title || "正在读取媒体信息"}</p></div>
                  <div><label>简介</label><p>{activeTask.description || "暂无简介"}</p></div>
                  <div className="prototype-review-meta">
                    <span><b>投稿平台</b><PlatformChips platforms={activeTask.target_platforms} /></span>
                    <label><b>转载声明</b><select value={activeTask.repost_statement_version} disabled={statement.isPending} onChange={(event) => statement.mutate({ task: activeTask, version: event.target.value as StatementVersion })}><option value="brief_v1">简洁版 v1</option><option value="full_v1">完整版 v1</option></select></label>
                  </div>
                  <div className="prototype-review-check"><Icon name="review" /><span>媒体、元数据和字幕产物已汇总，可进入审核台完成判定。</span></div>
                </> : null}
                {activeTab === "subtitles" ? <section className="prototype-review-tab-panel"><header><strong>字幕全文</strong><span>{activeReview.data?.subtitles.length ?? 0} 个字幕版本</span></header>{activeReview.isPending ? <LoadingBlock label="正在读取字幕" /> : subtitleText ? <pre>{subtitleText}</pre> : <EmptyState title="没有可预览的字幕" description="当前任务尚未生成字幕文档。" />}</section> : null}
                {activeTab === "media" ? <section className="prototype-review-tab-panel"><header><strong>封面与媒体</strong><span>{activeTask.assets.filter((asset) => asset.status !== "deleted").length} 个文件</span></header><div className="prototype-review-assets">{activeTask.assets.filter((asset) => asset.status !== "deleted").map((asset) => <a key={asset.id} href={api.assetContentURL(activeTask.id, asset.id)} target="_blank" rel="noreferrer"><Icon name={asset.content_type.startsWith("video/") ? "media" : "file"} /><span><strong>{asset.original_name || assetKindLabel(asset.kind)}</strong><small>{assetKindLabel(asset.kind)} · {formatBytes(asset.size_bytes)}</small></span></a>)}</div></section> : null}
                {activeTab === "history" ? <section className="prototype-review-tab-panel"><header><strong>审核记录</strong><span>{(activeReview.data?.actions.length ?? 0) + activeTask.steps.length} 条记录</span></header><ol className="prototype-review-history">{(activeReview.data?.actions ?? []).map((item) => <li key={item.id}><strong>{item.action === "approve" ? "审核通过" : item.action === "request_changes" ? "驳回修改" : "审核操作"}</strong><span>{item.reason || "未填写说明"}</span><time>{formatDateTime(item.created_at)}</time></li>)}{activeTask.steps.map((step) => <li key={step.kind}><strong>{stepLabel(step.kind)}</strong><span>{statusLabel(step.status)}</span><time>{formatDateTime(step.updated_at)}</time></li>)}</ol></section> : null}
              </div>
              <footer className="prototype-review-actions">
                <button className="button button-primary prototype-review-approve" type="button" disabled={action.isPending} onClick={() => action.mutate({ taskID: activeTask.id, kind: "approve", reason: "审核队列快捷通过" })}>{action.isPending ? "正在处理…" : "通过并投稿"}<kbd>Enter</kbd></button>
                <Link className="button button-secondary" to={`/reviews/${activeTask.id}`}>修改后通过</Link>
                <button className="button button-danger" type="button" disabled={action.isPending} onClick={() => setRejectOpen(true)}>驳回</button>
              </footer>
            </section>
          </div>
        </>
      ) : null}

      <ConfirmDialog open={rejectOpen} title="驳回当前任务" description="任务将退回修改，审核意见会写入审计记录。" confirmLabel="确认驳回" destructive busy={action.isPending} confirmDisabled={!rejectReason.trim() || !activeTask} onClose={() => { if (!action.isPending) setRejectOpen(false); }} onConfirm={() => { if (activeTask && rejectReason.trim()) action.mutate({ taskID: activeTask.id, kind: "request_changes", reason: rejectReason.trim() }); }}>
        <label className="field review-reject-field"><span>驳回原因</span><textarea autoFocus rows={4} value={rejectReason} onChange={(event) => setRejectReason(event.target.value)} placeholder="请说明需要修改的内容" /></label>
      </ConfirmDialog>
    </div>
  );
}
