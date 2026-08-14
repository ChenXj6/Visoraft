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
  TaskTitle
} from "../components";
import { formatDateTime, taskStatusForDisplay } from "../format";

function messageOf(error: unknown) {
  return error instanceof ApiError ? error.message : "无法读取投稿任务";
}

export default function PublishingQueuePage() {
  const tasks = useQuery({
    queryKey: ["tasks"],
    queryFn: api.tasks,
    refetchInterval: 5_000
  });

  if (tasks.isPending) return <LoadingBlock label="正在读取投稿队列" />;
  if (tasks.isError || !tasks.data) {
    return (
      <QueryError
        title="无法读取投稿队列"
        message={messageOf(tasks.error)}
        retry={() => void tasks.refetch()}
      />
    );
  }

  const items = tasks.data.items.filter(
    (task) =>
      Boolean(task.publish_job_id) ||
      ["ready_to_publish", "publishing", "published", "reconciled"].includes(
        task.status
      )
  );

  return (
    <>
      <PageHeader
        title="投稿"
        description="审核通过后的任务会留在这里，直到各平台上传、回查和失败恢复全部结束。"
        actions={
          <Link className="button button-secondary" to="/settings?section=publishing">
            投稿配置
          </Link>
        }
      />

      {items.length === 0 ? (
        <EmptyState
          title="暂无待投稿任务"
          description="人工或自动审核通过后，任务会进入此列表，不会从系统中消失。"
          action={
            <Link className="button button-primary" to="/tasks">
              查看全部任务
            </Link>
          }
        />
      ) : (
        <div className="publishing-queue">
          {items.map((task) => (
            <article className="work-panel publishing-queue-item" key={task.id}>
              <div className="publishing-queue-main">
                <div className="publishing-queue-title">
                  <PlatformChips platforms={task.target_platforms} />
                  <StatusBadge
                    status={
                      task.publish_mode === "simulation"
                        ? "simulated"
                        : task.publish_status || taskStatusForDisplay(task)
                    }
                  />
                </div>
                <h2>
                  <Link to={`/publishing/${task.id}`}>
                    <TaskTitle task={task} />
                  </Link>
                </h2>
                <p>
                  {task.status === "ready_to_publish"
                    ? "审核已通过，等待核对投稿草稿并入队。"
                    : task.status === "publishing"
            ? "正在上传或等待平台处理。"
                      : task.publish_mode === "simulation"
                        ? "仅完成本地模拟，没有向目标平台提交。"
                        : task.status === "published"
                          ? "目标平台已返回稿件编号；是否公开仍以平台审核状态为准。"
                        : "打开投稿工作台查看平台状态。"}
                </p>
              </div>
              <div className="publishing-queue-meta">
                <span>更新于 {formatDateTime(task.updated_at)}</span>
                <Link className="button button-primary" to={`/publishing/${task.id}`}>
                  打开投稿工作台
                </Link>
              </div>
            </article>
          ))}
        </div>
      )}
    </>
  );
}
