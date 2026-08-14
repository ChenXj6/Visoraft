import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, ApiError } from "../api";
import {
  EmptyState,
  LoadingBlock,
  PageHeader,
  PlatformChips,
  QueryError,
  StatusBadge,
  TaskTitle,
  WorkflowRail
} from "../components";
import { formatRelativeTime, shortID } from "../format";

export default function ReviewQueuePage() {
  const reviews = useQuery({
    queryKey: ["reviews"],
    queryFn: api.reviews,
    refetchInterval: 5_000
  });

  return (
    <>
      <PageHeader
        title="媒体复核台"
        description="这里只显示需要人工处理的任务。自动审核的规则结果也会留在任务审计中。"
      />

      {reviews.isPending && <LoadingBlock label="正在读取审核队列" />}
      {reviews.isError && (
        <QueryError
          title="审核队列暂时不可用"
          message={
            reviews.error instanceof ApiError
              ? reviews.error.message
        : "暂时无法连接审核服务"
          }
          retry={() => void reviews.refetch()}
        />
      )}
      {reviews.data?.items.length === 0 && (
        <EmptyState
          title="当前没有待人工审核任务"
          description="手动审核任务或自动审核失败转人工的任务会出现在这里。"
          action={
            <Link className="button button-secondary" to="/tasks">
              查看全部任务
            </Link>
          }
        />
      )}

      <div className="review-queue">
        {reviews.data?.items.map((task) => (
          <article className="review-card" key={task.id}>
            <div className="review-poster">
              {task.thumbnail_url ? (
                <img src={task.thumbnail_url} alt="" loading="lazy" />
              ) : (
                <span aria-hidden="true">NO PREVIEW</span>
              )}
              <small>{task.duration_seconds ? `${task.duration_seconds}s` : "--:--"}</small>
            </div>
            <div className="review-card-body">
              <header>
                <p className="track-kicker">
                  <span className="timecode">#{shortID(task.id)}</span>
                  <span>{formatRelativeTime(task.updated_at)}</span>
                </p>
                <h2>
                  <TaskTitle task={task} />
                </h2>
                <p>{task.description || "暂无简介"}</p>
              </header>
              <WorkflowRail task={task} compact />
              <dl className="review-brief">
                <div>
                  <dt>审核来源</dt>
                  <dd>{task.review_mode === "automatic" ? "自动规则转人工" : "手动策略"}</dd>
                </div>
                <div>
                  <dt>当前状态</dt>
                  <dd>
                    {task.review_status === "changes_requested" ? "已退回修改" : "等待判定"}
                  </dd>
                </div>
                <div>
                  <dt>字幕产物</dt>
                  <dd>{task.assets.filter((asset) => asset.kind.startsWith("subtitle_")).length}</dd>
                </div>
              </dl>
            </div>
            <div className="review-card-actions">
              <PlatformChips platforms={task.target_platforms} />
              <StatusBadge status={task.status} />
              <Link className="button button-primary" to={`/reviews/${task.id}`}>
                打开审核台
              </Link>
            </div>
          </article>
        ))}
      </div>
    </>
  );
}
