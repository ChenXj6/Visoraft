import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import {
  api,
  ApiError,
  type Platform,
  type PlatformAccount,
  type PlatformCategory,
  type PlatformPublication,
  type PostingStrategy,
  type PublishingDetail
} from "../api";
import {
  LoadingBlock,
  PageHeader,
  PlatformChips,
  QueryError,
  StatusBadge
} from "../components";
import {
  formatDateTime,
  friendlyErrorMessage,
  statusLabel,
  taskStatusForDisplay
} from "../format";
import { PublicationRecoveryPanel } from "./PublicationRecoveryPanel";
import { TransientNotice } from "../product-ui";

const platformName: Record<Platform, string> = {
  acfun: "AcFun",
  bilibili: "Bilibili"
};

const publicationStageLabels: Record<string, string> = {
  queued: "等待执行",
  preparing: "准备投稿文件",
  uploading: "上传视频",
  uploading_cover: "上传封面",
  submitting: "提交稿件",
  reconciling: "核对平台结果",
  completed: "执行完成",
  failed: "执行失败"
};

function publicationStageLabel(stage: string) {
  return publicationStageLabels[stage] ?? "平台投稿处理";
}

const blockerActionLabels: Record<string, string> = {
  select_posting_strategy: "选择投稿策略",
  enable_posting_strategy: "启用投稿策略",
  run_transcode: "执行视频转码",
  retry_media_processing: "重试媒体处理",
  prepare_cover: "准备视频封面",
  run_content_moderation: "执行内容安全审核",
  edit_posting_strategy: "修改投稿策略",
  bind_platform_account: "绑定平台账号",
  check_platform_account: "重新校验平台账号",
  bind_real_platform_account: "绑定真实平台账号",
  select_platform_category: "选择投稿分区"
};

const nextActionCopy: Record<string, { title: string; description: string }> = {
  prepare: {
    title: "生成投稿草稿",
    description: "审核已完成。先根据任务快照生成每个平台的投稿草稿，再核对账号、分区和文案。"
  },
  prepare_publish_job: {
    title: "生成投稿草稿",
    description: "审核已完成。先根据任务快照生成每个平台的投稿草稿，再核对账号、分区和文案。"
  },
  resolve_blockers: {
    title: "处理投稿阻塞项",
    description: "当前配置还不能安全入队。按下方阻塞说明补齐账号、分区、媒体或投稿策略。"
  },
  review_publish_draft: {
    title: "核对各平台投稿草稿",
    description: "草稿已经生成。确认标题、简介、标签、账号和分区后，再加入发布队列。"
  },
  track_platform_publications: {
    title: "发布任务正在执行",
    description: "正在上传或等待平台处理，页面会自动刷新状态。"
  },
  retry_failed_platforms: {
    title: "处理失败的平台",
    description: "已成功的平台不会重复投稿；查看失败原因后，仅重试需要恢复的平台。"
  },
  view_publish_results: {
    title: "平台已接收投稿",
    description: "下方可核对平台返回的稿件编号和地址；是否公开仍以平台审核状态为准。"
  },
  prepare_new_publish_job: {
    title: "当前发布任务已取消",
    description: "历史记录仍保留；如需再次投稿，应生成新的发布任务和幂等指纹。"
  },
  open_publish_detail: {
    title: "查看投稿状态",
    description: "平台投稿和尝试记录均已持久化，页面会持续刷新最新状态。"
  }
};

function messageOf(error: unknown, fallback: string) {
  return error instanceof ApiError ? error.message : fallback;
}

function humanNextAction(value: string) {
  return (
    nextActionCopy[value] ?? {
      title: value ? "等待下一步操作" : "等待投稿状态",
      description: "发布状态已持久化，可返回任务详情继续追踪。"
    }
  );
}

function blockerRoute(action: string, taskId: string) {
  if (["select_posting_strategy", "enable_posting_strategy", "edit_posting_strategy", "bind_platform_account", "check_platform_account", "bind_real_platform_account", "select_platform_category"].includes(action)) return "/publishing/settings";
  if (["run_transcode", "retry_media_processing", "prepare_cover"].includes(action)) return `/tasks/${taskId}`;
  if (action === "run_content_moderation") return "/settings?section=moderation";
  return `/tasks/${taskId}`;
}

function isFixtureSource(sourceURL: string) {
  try {
    return new URL(sourceURL).hostname.toLowerCase() === "fixture-provider";
  } catch {
    return false;
  }
}

function PlatformDraftEditor({
  taskId,
  publication,
  accounts,
  categories,
  attempts,
  onUpdated,
  onRefresh
}: {
  taskId: string;
  publication: PlatformPublication;
  accounts: PlatformAccount[];
  categories: PlatformCategory[];
  attempts: PublishingDetail["attempts"][string];
  onUpdated: (detail: PublishingDetail, message: string) => Promise<void>;
  onRefresh: () => void;
}) {
  const [accountId, setAccountId] = useState(publication.account_id);
  const [categoryId, setCategoryId] = useState(publication.category_id);
  const [title, setTitle] = useState(publication.title);
  const [description, setDescription] = useState(publication.description);
  const [tags, setTags] = useState(publication.tags.join(", "));
  const selectedAccount = accounts.find((account) => account.id === accountId);
  const selectedSimulation = selectedAccount?.auth_mode === "fixture";
  const cardStatus =
    publication.status === "published" && publication.simulation
      ? "simulated"
      : publication.status === "published" &&
          publication.remote_status === "submitted"
        ? "submitted"
        : publication.status;

  useEffect(() => {
    setAccountId(publication.account_id);
    setCategoryId(publication.category_id);
    setTitle(publication.title);
    setDescription(publication.description);
    setTags(publication.tags.join(", "));
  }, [publication.id, publication.version]);

  const save = useMutation({
    mutationFn: () =>
      api.updatePublishingDraft(taskId, publication.platform, {
        expected_version: publication.version,
        account_id: accountId,
        category_id: categoryId,
        title: title.trim(),
        description: description.trim(),
        tags: tags
          .split(/[,，\n]/)
          .map((item) => item.trim())
          .filter(Boolean)
      }),
    onSuccess: (detail) =>
      onUpdated(detail, `${platformName[publication.platform]} 投稿草稿已保存。`)
  });

  const retry = useMutation({
    mutationFn: () =>
      api.retryPlatformPublishing(taskId, publication.platform),
    onSuccess: (detail) =>
      onUpdated(detail, `${platformName[publication.platform]} 已重新加入发布队列。`)
  });

  const busy = save.isPending || retry.isPending;
  const canEdit = ["draft", "blocked", "failed"].includes(publication.status);
  const canRetry = publication.status === "failed" && !publication.simulation;

  return (
    <section className="work-panel publication-card">
      <header className="publication-card-head">
        <div>
          <p className="eyebrow">投稿平台</p>
          <h2>{platformName[publication.platform]} 投稿</h2>
          <p>
            第 {publication.attempt || 0} 次尝试
                {publication.adapter_version ? " · 平台连接已记录" : ""}
          </p>
        </div>
        <StatusBadge status={cardStatus} />
      </header>

      {(publication.simulation || selectedSimulation) && (
        <div className="publication-simulation-notice" role="note">
          <strong>本地模拟账号，不会向 {platformName[publication.platform]} 投稿</strong>
          <span>
            {publication.status === "published"
              ? "这条历史记录只完成了本地演示验证，没有在平台创建稿件。"
              : "请选择标有“Cookie 认证”的真实账号并保存草稿，才能进入真实发布队列。"}
          </span>
        </div>
      )}

      <div className="publication-form-grid">
        <label className="field">
          <span>投稿账号</span>
          <select
            value={accountId}
            disabled={!canEdit || busy}
            onChange={(event) => setAccountId(event.target.value)}
          >
            <option value="">请选择已验证账号</option>
            {accounts.map((account) => (
              <option value={account.id} key={account.id}>
                {account.name}
                {account.remote_display_name
                  ? ` · ${account.remote_display_name}`
                  : ""}
                {account.status !== "ready" ? ` · ${statusLabel(account.status)}` : ""}
                {account.auth_mode === "fixture"
                  ? " · 本地测试（不会投稿）"
                  : " · Cookie 认证"}
              </option>
            ))}
          </select>
        </label>

        <label className="field">
          <span>投稿分区</span>
          <select
            value={categoryId}
            disabled={!canEdit || busy}
            onChange={(event) => setCategoryId(event.target.value)}
          >
            <option value="">请选择平台分区</option>
            {categories.map((category) => (
              <option value={category.category_id} key={category.category_id}>
                {category.path || category.name}
              </option>
            ))}
          </select>
        </label>

        <label className="field field-wide">
          <span>标题</span>
          <input
            value={title}
            disabled={!canEdit || busy}
            onChange={(event) => setTitle(event.target.value)}
          />
        </label>

        <label className="field field-wide">
          <span>简介与转载声明</span>
          <textarea
            rows={7}
            value={description}
            disabled={!canEdit || busy}
            onChange={(event) => setDescription(event.target.value)}
          />
        </label>

        <label className="field field-wide">
          <span>标签（逗号分隔）</span>
          <input
            value={tags}
            disabled={!canEdit || busy}
            onChange={(event) => setTags(event.target.value)}
          />
        </label>
      </div>

      {(save.isError || retry.isError) && (
        <p className="publication-error" role="alert">
          {messageOf(save.error ?? retry.error, "投稿操作失败")}
        </p>
      )}

      {publication.status === "reconciliation_required" && (
        <PublicationRecoveryPanel
          taskId={taskId}
          publication={publication}
          onResolved={onUpdated}
          onRefresh={onRefresh}
        />
      )}

      <div className="publication-actions">
        <button
          className="button button-secondary"
          type="button"
          disabled={!canEdit || busy}
          onClick={() => save.mutate()}
        >
          {save.isPending ? "正在保存…" : "保存平台草稿"}
        </button>
        {canRetry && (
          <button
            className="button button-primary"
            type="button"
            disabled={busy}
            onClick={() => retry.mutate()}
          >
            {retry.isPending ? "正在重试…" : "重试该平台"}
          </button>
        )}
        {publication.remote_url && !publication.simulation && (
          <a
            className="button button-secondary"
            href={publication.remote_url}
            target="_blank"
            rel="noreferrer"
          >
            打开平台返回地址
          </a>
        )}
      </div>

      {(publication.remote_submission_id ||
        publication.remote_status ||
        publication.error_message) && (
        <dl className="publication-result">
          {publication.remote_submission_id && (
            <div>
              <dt>{publication.simulation ? "本地模拟编号" : "平台稿件编号"}</dt>
              <dd>{publication.remote_submission_id}</dd>
            </div>
          )}
          {publication.remote_status && (
            <div>
              <dt>{publication.simulation ? "模拟状态" : "平台状态"}</dt>
              <dd>
                {publication.simulation
                  ? "本地模拟完成（未提交平台）"
                  : publication.remote_status === "submitted"
                    ? "平台已接收，等待平台审核或公开"
                    : publication.remote_status}
              </dd>
            </div>
          )}
          {publication.error_message && (
            <div>
              <dt>失败原因</dt>
              <dd>{friendlyErrorMessage(publication.error_message)}</dd>
            </div>
          )}
        </dl>
      )}

      {attempts?.length > 0 && (
        <details className="publication-attempts">
          <summary>查看 {attempts.length} 条执行记录</summary>
          <ol>
            {attempts.map((attempt) => (
              <li key={attempt.id}>
                <div>
                  <strong>
                    第 {attempt.attempt} 次 · {publicationStageLabel(attempt.stage)}
                  </strong>
                  <StatusBadge status={attempt.status} />
                </div>
                <p>
                  {attempt.error_message ||
                    (publication.simulation ? "本地模拟步骤完成，未连接平台" : "步骤执行完成")}
                </p>
                <time>{formatDateTime(attempt.started_at)}</time>
              </li>
            ))}
          </ol>
        </details>
      )}
    </section>
  );
}

export default function PublishingPage() {
  const { taskId = "" } = useParams();
  const queryClient = useQueryClient();
  const [notice, setNotice] = useState("");
  const [selectedStrategyID, setSelectedStrategyID] = useState("");

  const task = useQuery({
    queryKey: ["task", taskId],
    queryFn: () => api.task(taskId),
    enabled: Boolean(taskId),
    refetchInterval: 5_000
  });
  const publishing = useQuery({
    queryKey: ["publishing", taskId],
    queryFn: () => api.publishing(taskId),
    enabled: Boolean(taskId),
    refetchInterval: 5_000
  });
  const accounts = useQuery({
    queryKey: ["platform-accounts"],
    queryFn: () => api.platformAccounts()
  });
  const categories = useQuery({
    queryKey: ["platform-categories"],
    queryFn: async () => {
      const [acfun, bilibili] = await Promise.all([
        api.platformCategories("acfun"),
        api.platformCategories("bilibili")
      ]);
      return { items: [...acfun.items, ...bilibili.items] };
    }
  });
  const strategies = useQuery({
    queryKey: ["posting-strategies"],
    queryFn: api.postingStrategies,
  });

  const refresh = async (detail: PublishingDetail, message: string) => {
    queryClient.setQueryData(["publishing", taskId], detail);
    setNotice(message);
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["task", taskId] }),
      queryClient.invalidateQueries({ queryKey: ["tasks"] }),
      queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
      queryClient.invalidateQueries({ queryKey: ["publishing-queue"] })
    ]);
  };

  const prepare = useMutation({
    mutationFn: () => api.preparePublishing(taskId),
    onSuccess: (detail) => refresh(detail, "投稿草稿已经生成，请逐个平台核对。"),
    onError: (error) =>
      setNotice(messageOf(error, "暂时无法生成投稿草稿"))
  });
  const enqueue = useMutation({
    mutationFn: () => api.enqueuePublishing(taskId),
    onSuccess: (detail) =>
      refresh(detail, "投稿任务已进入发布队列，将按平台依次执行。"),
    onError: (error) =>
      setNotice(messageOf(error, "暂时无法加入发布队列"))
  });
  const assignStrategy = useMutation({
    mutationFn: async (strategyID: string) => {
      const updatedTask = await api.setTaskPostingStrategy(taskId, strategyID);
      queryClient.setQueryData(["task", taskId], updatedTask);
      return api.preparePublishing(taskId);
    },
    onSuccess: (detail) => refresh(detail, "投稿策略已应用，发布条件已重新检查。"),
    onError: (error) => setNotice(messageOf(error, "投稿策略应用失败")),
  });

  useEffect(() => {
    if (selectedStrategyID) return;
    const enabled = (strategies.data?.items ?? []).filter((item) => item.enabled);
    const preferred = enabled.find((item) => item.id === task.data?.posting_strategy_id) ?? enabled[0];
    if (preferred) setSelectedStrategyID(preferred.id);
  }, [selectedStrategyID, strategies.data?.items, task.data?.posting_strategy_id]);

  const accountByPlatform = useMemo(() => {
    const grouped: Record<Platform, PlatformAccount[]> = {
      acfun: [],
      bilibili: []
    };
    for (const account of accounts.data?.items ?? []) {
      grouped[account.platform].push(account);
    }
    return grouped;
  }, [accounts.data]);

  const categoryByPlatform = useMemo(() => {
    const grouped: Record<Platform, PlatformCategory[]> = {
      acfun: [],
      bilibili: []
    };
    for (const category of categories.data?.items ?? []) {
      if (category.active) grouped[category.platform].push(category);
    }
    return grouped;
  }, [categories.data]);

  if (task.isPending || publishing.isPending) {
    return <LoadingBlock label="正在打开投稿工作台" />;
  }
  if (task.isError || !task.data) {
    return (
      <QueryError
        title="无法读取任务"
        message={messageOf(task.error, "任务不存在或任务服务暂不可用")}
        retry={() => void task.refetch()}
      />
    );
  }
  if (publishing.isError || !publishing.data) {
    return (
      <QueryError
        title="无法读取投稿状态"
        message={messageOf(publishing.error, "投稿状态不存在或发布服务暂不可用")}
        retry={() => void publishing.refetch()}
      />
    );
  }

  const detail = publishing.data;
  const fixtureSource = isFixtureSource(task.data.source_url);
  const hasSimulationPublication = detail.publications.some((item) => item.simulation);
  const hasForbiddenSimulation = !fixtureSource && hasSimulationPublication;
  const hasUncertainPublication = detail.publications.some(
    (item) => item.status === "reconciliation_required"
  );
  const next = hasForbiddenSimulation && detail.job?.status === "published"
    ? {
        title: "仅完成本地模拟，没有向平台投稿",
        description:
          "当前记录使用本地测试账号。切换为已校验的 Cookie 认证账号后，重新生成或保存投稿草稿。"
      }
    : hasUncertainPublication
    ? {
        title: "先核验平台是否已经生成稿件",
        description:
          "至少一个平台的提交结果不确定。请在下方平台核验台确认远端结果，系统不会在未核验时盲目重投。"
      }
    : humanNextAction(detail.next_action);
  const canEnqueue =
    Boolean(detail.job) &&
    !hasUncertainPublication &&
    !hasForbiddenSimulation &&
    detail.blockers.length === 0 &&
    detail.publications.length > 0 &&
    Boolean(
      detail.job &&
        ["draft", "failed", "partial_success"].includes(detail.job.status)
    ) &&
    detail.publications.some((item) =>
      ["draft", "failed"].includes(item.status)
    );
  const busy = prepare.isPending || enqueue.isPending || assignStrategy.isPending;
  const needsStrategy = detail.blockers.some((blocker) => blocker.action === "select_posting_strategy" || blocker.code === "posting_strategy_missing");
  const usableStrategies = (strategies.data?.items ?? []).filter((strategy: PostingStrategy) => strategy.enabled && task.data!.target_platforms.every((platform) => strategy.target_platforms.includes(platform)));

  return (
    <>
      <PageHeader
        title="投稿工作台"
        description={task.data.title || task.data.original_title || task.data.source_url}
        actions={
          <div className="review-heading-actions">
            <PlatformChips platforms={task.data.target_platforms} />
            <StatusBadge status={taskStatusForDisplay(task.data)} />
            <Link className="button button-secondary" to={`/tasks/${taskId}`}>
              返回任务
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

      <section className="publishing-next-step" aria-labelledby="publishing-next-title">
        <div>
          <span>当前下一步</span>
          <h2 id="publishing-next-title">{next.title}</h2>
          <p>{next.description}</p>
        </div>
        <div className="publishing-next-actions">
          {!detail.job && (
            <button
              className="button button-primary"
              type="button"
              disabled={busy}
              onClick={() => prepare.mutate()}
            >
              {prepare.isPending ? "正在生成…" : "生成投稿草稿"}
            </button>
          )}
          {detail.job && (
            <button
              className="button button-primary"
              type="button"
              disabled={!canEnqueue || busy}
              onClick={() => enqueue.mutate()}
            >
              {enqueue.isPending ? "正在入队…" : "确认并加入发布队列"}
            </button>
          )}
          {detail.job && detail.blockers.length > 0 ? (
            <button className="button button-secondary" type="button" disabled={busy} onClick={() => prepare.mutate()}>
              {prepare.isPending ? "正在检查…" : "重新检查发布条件"}
            </button>
          ) : null}
          <Link className="button button-secondary" to="/publishing/settings">
            账号与策略
          </Link>
          <Link className="button button-secondary" to="/settings?section=publishing">
            发布参数
          </Link>
        </div>
      </section>

      {detail.blockers.length > 0 && (
        <section className="publishing-blockers" aria-labelledby="publishing-blockers-title">
          <header>
            <h2 id="publishing-blockers-title">还不能发布</h2>
            <p>以下项目全部解决后，入队按钮才会启用。</p>
          </header>
          {needsStrategy ? (
            <div className="publishing-strategy-picker">
              <label className="field">
                <span>为当前任务选择投稿策略</span>
                <select value={selectedStrategyID} disabled={assignStrategy.isPending} onChange={(event) => setSelectedStrategyID(event.target.value)}>
                  <option value="">请选择可用策略</option>
                  {usableStrategies.map((strategy) => <option key={strategy.id} value={strategy.id}>{strategy.name} · {strategy.automation_mode === "automatic_after_review" ? "审核后自动" : "审核后手动"}</option>)}
                </select>
              </label>
              <button className="button button-primary" type="button" disabled={!selectedStrategyID || assignStrategy.isPending} onClick={() => assignStrategy.mutate(selectedStrategyID)}>
                {assignStrategy.isPending ? "正在应用…" : "应用策略并重新检查"}
              </button>
              {usableStrategies.length === 0 ? <Link className="button button-secondary" to="/publishing/settings">新建可用策略</Link> : null}
            </div>
          ) : null}
          <ul>
            {detail.blockers.map((blocker, index) => (
              <li key={`${blocker.code}-${blocker.platform ?? "all"}-${index}`}>
                <strong>
                  {blocker.platform
                    ? `${platformName[blocker.platform as Platform] ?? "未知平台"} · `
                    : ""}
                  {blocker.message}
                </strong>
                <Link className="publishing-blocker-action" to={blockerRoute(blocker.action, taskId)}>{blockerActionLabels[blocker.action] ?? "处理当前阻塞项"}</Link>
              </li>
            ))}
          </ul>
        </section>
      )}

      {detail.job && (
        <section className="publishing-job-summary" aria-label="发布任务状态">
          <div>
            <span>发布任务</span>
            <strong>{detail.job.id}</strong>
          </div>
          <div>
            <span>任务状态</span>
            <StatusBadge status={detail.job.status} />
          </div>
          <div>
            <span>启动方式</span>
            <strong>{detail.job.auto_started ? "审核后自动" : "人工确认"}</strong>
          </div>
          <div>
            <span>排队时间</span>
            <strong>
              {detail.job.queued_at ? formatDateTime(detail.job.queued_at) : "尚未入队"}
            </strong>
          </div>
        </section>
      )}

      <div className="publication-list">
        {detail.publications.map((publication) => (
          <PlatformDraftEditor
            key={publication.id}
            taskId={taskId}
            publication={publication}
            accounts={accountByPlatform[publication.platform]}
            categories={categoryByPlatform[publication.platform]}
            attempts={detail.attempts[publication.id] ?? []}
            onUpdated={refresh}
            onRefresh={() => void publishing.refetch()}
          />
        ))}
      </div>

      {!detail.job && (
        <section className="work-panel publishing-empty">
          <h2>审核记录仍然保留</h2>
          <p>
            当前任务状态为“{statusLabel(task.data.status)}”。生成投稿草稿不会立即上传，只会冻结账号、分区、
            标题、简介、标签和媒体版本，便于发布前核对。
          </p>
        </section>
      )}
    </>
  );
}
