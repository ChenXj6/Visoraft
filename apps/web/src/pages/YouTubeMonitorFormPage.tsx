import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  api,
  ApiError,
  type Platform,
  type YouTubeMonitorInput
} from "../api";
import { LoadingBlock, PageHeader, QueryError } from "../components";
import { statusLabel } from "../format";
import { Icon, type IconName } from "../icons";
import { TransientNotice } from "../product-ui";

type ListField =
  | "channel_ids"
  | "include_keywords"
  | "exclude_keywords"
  | "exclude_channel_ids";

const initialValue: YouTubeMonitorInput = {
  name: "",
  enabled: true,
  monitor_type: "search",
  channel_mode: "latest",
  query: "",
  series_title: "",
  series_scopes: [],
  episode_start: 0,
  episode_end: 0,
  channel_ids: [],
  include_keywords: [],
  exclude_keywords: [],
  exclude_channel_ids: [],
  region_code: "US",
  category_id: "",
  lookback_days: 7,
  max_results: 25,
  order_by: "date",
  video_types: ["video", "short"],
  min_view_count: 0,
  min_like_count: 0,
  min_comment_count: 0,
  min_duration_seconds: 0,
  max_duration_seconds: 0,
  schedule_type: "automatic",
  schedule_interval_minutes: 60,
  rate_limit_requests: 10,
  auto_add_to_tasks: false,
  task_template: {
    target_platforms: ["bilibili"],
    repost_statement_version: "brief_v1",
    auto_publish: false
  }
};

function seriesEpisodeCount(scopes: YouTubeMonitorInput["series_scopes"]) {
  return scopes.reduce(
    (total, scope) => total + Math.max(0, scope.episode_end - scope.episode_start + 1),
    0
  );
}

function recommendedSeriesRequestBudget(scopes: YouTubeMonitorInput["series_scopes"]) {
  return Math.min(250, Math.max(2, scopes.length * 2 + seriesEpisodeCount(scopes) * 2));
}

function message(error: unknown, fallback: string) {
  return error instanceof ApiError ? error.message : fallback;
}

function scheduleIntervalLabel(minutes: number) {
  if (minutes === 30) return "每 30 分钟";
  if (minutes === 60) return "每小时";
  if (minutes === 1440) return "每天";
  if (minutes % 60 === 0) return `每 ${minutes / 60} 小时`;
  return `每 ${minutes} 分钟`;
}

function splitList(value: string) {
  return value
    .split(/[,，、;；\n]/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function FieldGroup({
  icon,
  title,
  description,
  children
}: {
  icon: IconName;
  title: string;
  description: string;
  children: ReactNode;
}) {
  return (
    <section className="work-panel monitor-form-section">
      <header className="section-heading">
        <span className="sequence-mark"><Icon name={icon} /></span>
        <div>
          <h2>{title}</h2>
          <p>{description}</p>
        </div>
      </header>
      {children}
    </section>
  );
}

function CheckCard({
  checked,
  label,
  note,
  disabled = false,
  onChange
}: {
  checked: boolean;
  label: string;
  note?: string;
  disabled?: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <label className={`check-card ${disabled ? "check-card-disabled" : ""}`}>
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
      />
      <span aria-hidden="true" />
      <div>
        <strong>{label}</strong>
        {note && <small>{note}</small>}
      </div>
    </label>
  );
}

export default function YouTubeMonitorFormPage() {
  const { monitorId } = useParams();
  const editing = Boolean(monitorId);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<YouTubeMonitorInput>(initialValue);
  const [version, setVersion] = useState(0);
  const [listText, setListText] = useState<Record<ListField, string>>({
    channel_ids: "",
    include_keywords: "",
    exclude_keywords: "",
    exclude_channel_ids: ""
  });
  const [notice, setNotice] = useState("");
  const [fields, setFields] = useState<Record<string, string>>({});
  const [advancedOpen, setAdvancedOpen] = useState(false);

  const monitor = useQuery({
    queryKey: ["youtube-monitor", monitorId],
    queryFn: () => api.youtubeMonitor(monitorId ?? ""),
    enabled: editing
  });
  const cookies = useQuery({
    queryKey: ["cookie-profiles"],
    queryFn: api.cookieProfiles
  });
  const strategies = useQuery({
    queryKey: ["posting-strategies"],
    queryFn: api.postingStrategies
  });
  const youtubeCategories = useQuery({
    queryKey: ["youtube-categories", draft.region_code],
    queryFn: () => api.youtubeCategories(draft.region_code),
    enabled: draft.region_code.trim().length === 2
  });

  useEffect(() => {
    if (!monitor.data) return;
    const {
      id: _id,
      state: _state,
      last_run_at: _lastRun,
      next_run_at: _nextRun,
      last_error: _lastError,
      version: nextVersion,
      created_at: _created,
      updated_at: _updated,
      ...input
    } = monitor.data;
    setDraft({
      ...input,
      series_scopes:
        input.series_scopes.length > 0
          ? input.series_scopes
          : [{
              key: "part-1",
              name: "",
              query: input.query,
              episode_start: input.episode_start,
              episode_end: input.episode_end
            }]
    });
    setVersion(nextVersion);
    setListText({
      channel_ids: input.channel_ids.join("\n"),
      include_keywords: input.include_keywords.join(", "),
      exclude_keywords: input.exclude_keywords.join(", "),
      exclude_channel_ids: input.exclude_channel_ids.join("\n")
    });
  }, [monitor.data]);

  const save = useMutation({
    mutationFn: () => {
      const input = {
        ...draft,
        channel_ids: splitList(listText.channel_ids),
        include_keywords: splitList(listText.include_keywords),
        exclude_keywords: splitList(listText.exclude_keywords),
        exclude_channel_ids: splitList(listText.exclude_channel_ids),
        published_after: draft.published_after || undefined,
        published_before: draft.published_before || undefined
      };
      return editing && monitorId
        ? api.updateYouTubeMonitor(monitorId, {
            ...input,
            expected_version: version
          })
        : api.createYouTubeMonitor(input);
    },
    onSuccess: async (value) => {
      await queryClient.invalidateQueries({ queryKey: ["youtube-monitors"] });
      navigate(`/monitors/${value.id}/history`, {
        replace: true,
        state: { notice: editing ? "监控配置已更新。" : "监控配置已创建。" }
      });
    },
    onError: (error) => {
      setNotice(message(error, "监控配置保存失败"));
      setFields(error instanceof ApiError ? error.fields ?? {} : {});
      setAdvancedOpen(true);
    }
  });

  const selectedVideos = useMemo(() => new Set(draft.video_types), [draft.video_types]);
  const selectedPlatforms = useMemo(
    () => new Set(draft.task_template.target_platforms),
    [draft.task_template.target_platforms]
  );
  const selectedPostingStrategy = strategies.data?.items.find(
    (strategy) =>
      strategy.id === draft.task_template.posting_strategy_id &&
      strategy.enabled
  );
  const totalSeriesEpisodes = seriesEpisodeCount(draft.series_scopes);
  const seriesRequestBudget = recommendedSeriesRequestBudget(draft.series_scopes);
  const currentIncludeKeywords = splitList(listText.include_keywords);
  const readinessIssues: string[] = [];
  if (!draft.name.trim()) readinessIssues.push("填写配置名称");
  if (draft.monitor_type === "search" && !draft.query.trim() && currentIncludeKeywords.length === 0) {
    readinessIssues.push("填写搜索词");
  }
  if (draft.monitor_type === "channel" && splitList(listText.channel_ids).length === 0) {
    readinessIssues.push("粘贴至少一个频道主页链接或 @账号");
  }
  if (draft.monitor_type === "series") {
    if (!draft.series_title.trim()) readinessIssues.push("填写节目名称");
    if (draft.series_scopes.some((scope) => scope.episode_start < 1 || scope.episode_end < scope.episode_start)) {
      readinessIssues.push("修正集数范围");
    }
    if (draft.rate_limit_requests < seriesRequestBudget) {
      readinessIssues.push(`将补漏请求上限提高到 ${seriesRequestBudget}`);
    }
  }

  const setNumber = (key: keyof YouTubeMonitorInput, value: number) =>
    setDraft((current) => ({ ...current, [key]: value }));
  const updateSeriesScope = (
    index: number,
    changes: Partial<YouTubeMonitorInput["series_scopes"][number]>
  ) => {
    setDraft((current) => {
      const seriesScopes = current.series_scopes.map((scope, scopeIndex) =>
        scopeIndex === index ? { ...scope, ...changes } : scope
      );
      return {
        ...current,
        series_scopes: seriesScopes,
        episode_start: Math.min(...seriesScopes.map((scope) => scope.episode_start)),
        episode_end: Math.max(...seriesScopes.map((scope) => scope.episode_end)),
        rate_limit_requests: recommendedSeriesRequestBudget(seriesScopes)
      };
    });
  };
  const selectMonitorType = (monitorType: YouTubeMonitorInput["monitor_type"]) => {
    if (monitorType === draft.monitor_type) return;
    setAdvancedOpen(false);
    setListText((current) => ({
      ...current,
      channel_ids: monitorType === "channel" ? current.channel_ids : "",
      include_keywords: ""
    }));
    setDraft((current) => {
      if (monitorType === "series") {
        const seriesScopes = current.series_scopes.length > 0
          ? current.series_scopes
          : [{ key: "part-1", name: "", query: "", episode_start: 1, episode_end: 24 }];
        return {
          ...current,
          monitor_type: monitorType,
          channel_mode: "historical",
          query: "",
          series_scopes: seriesScopes,
          episode_start: Math.min(...seriesScopes.map((scope) => scope.episode_start)),
          episode_end: Math.max(...seriesScopes.map((scope) => scope.episode_end)),
          max_results: 50,
          order_by: "relevance",
          video_types: ["video"],
          min_duration_seconds: current.min_duration_seconds || 1200,
          rate_limit_requests: recommendedSeriesRequestBudget(seriesScopes),
          schedule_type: "manual",
          auto_add_to_tasks: false
        };
      }
      return {
        ...current,
        monitor_type: monitorType,
        channel_mode: monitorType === "channel" ? "latest" : "search",
        query: "",
        series_title: "",
        series_scopes: [],
        episode_start: 0,
        episode_end: 0,
        max_results: 25,
        order_by: "date",
        video_types: ["video", "short"],
        min_duration_seconds: 0,
        max_duration_seconds: 0,
        rate_limit_requests: 10,
        schedule_type: "automatic",
        auto_add_to_tasks: false
      };
    });
  };
  const toggleVideo = (kind: "video" | "short" | "live") =>
    setDraft((current) => ({
      ...current,
      video_types: selectedVideos.has(kind)
        ? current.video_types.filter((item) => item !== kind)
        : [...current.video_types, kind]
    }));
  const togglePlatform = (platform: Platform) =>
    setDraft((current) => ({
      ...current,
      task_template: {
        ...current.task_template,
        target_platforms: selectedPlatforms.has(platform)
          ? current.task_template.target_platforms.filter((item) => item !== platform)
          : [...current.task_template.target_platforms, platform]
      }
    }));

  if (editing && monitor.isPending) {
    return <LoadingBlock label="正在读取监控配置" />;
  }
  if (editing && monitor.isError) {
    return (
      <QueryError
        title="无法打开监控配置"
        message={message(monitor.error, "监控配置不存在")}
        retry={() => void monitor.refetch()}
      />
    );
  }

  return (
    <>
      <PageHeader
        title={editing ? "编辑监控配置" : "建立发现规则"}
        description="先选择要找什么，再决定发现内容后的处理方式；不常用的筛选项已收进高级设置。"
        actions={
          <div className="page-actions">
            <Link className="button button-secondary" to="/monitors">
              返回列表
            </Link>
            <button
              className="button button-primary"
              type="button"
              disabled={save.isPending}
              onClick={() => {
                setNotice("");
                setFields({});
                save.mutate();
              }}
            >
              {save.isPending ? "正在保存…" : editing ? "保存新版本" : "创建监控"}
            </button>
          </div>
        }
      />

      {notice && (
        <TransientNotice tone="error" onDismiss={() => setNotice("")}>
          {notice}
        </TransientNotice>
      )}

      <section className="monitor-goal-selector" aria-label="选择监控方式">
        <header>
          <strong>你想监控什么？</strong>
          <span>三种方式共用同一套筛选、调度和任务流水线。</span>
        </header>
        <div>
          {([
            ["search", "关键词搜索", "搜索整个 YouTube，适合话题、人物和趋势"],
            ["channel", "指定频道", "粘贴频道主页链接或 @账号，持续跟踪更新"],
            ["series", "完整节目 / 剧集", "设置起止集数，系统逐集核对并自动补漏"]
          ] as const).map(([value, label, note]) => (
            <button
              type="button"
              key={value}
              className={draft.monitor_type === value ? "is-active" : ""}
              aria-pressed={draft.monitor_type === value}
              onClick={() => selectMonitorType(value)}
            >
              <span className="monitor-goal-radio" aria-hidden="true" />
              <strong>{label}</strong>
              <small>{note}</small>
            </button>
          ))}
        </div>
      </section>

      <div className="monitor-form-layout">
        <div className="monitor-form-main">
          <FieldGroup
            icon="monitor"
            title="监控目标"
            description="支持关键词、指定频道和按集号逐集核对的完整剧集监控。"
          >
            <div className="settings-form-grid">
              <label className="field field-wide">
                <span>配置名称</span>
                <input
                  value={draft.name}
                  onChange={(event) => setDraft({ ...draft, name: event.target.value })}
                  aria-invalid={Boolean(fields.name)}
                  placeholder="例如：设计频道每小时更新"
                />
                {fields.name && <small className="field-error">{fields.name}</small>}
              </label>
              {draft.monitor_type === "channel" && (
                <label className="field">
                  <span>频道模式</span>
                  <select
                    value={draft.channel_mode}
                    onChange={(event) =>
                      setDraft({
                        ...draft,
                        channel_mode: event.target.value as YouTubeMonitorInput["channel_mode"]
                      })
                    }
                  >
                    <option value="latest">持续发现最新内容</option>
                    <option value="historical">历史补录</option>
                    <option value="search">频道内关键词搜索</option>
                  </select>
                </label>
              )}
              {draft.monitor_type === "series" && (
                <>
                  <label className="field field-wide">
                    <span>节目名称</span>
                    <input
                      value={draft.series_title}
                      onChange={(event) =>
                        setDraft({ ...draft, series_title: event.target.value })
                      }
                      aria-invalid={Boolean(fields.series_title)}
                      placeholder="例如：还珠格格、权力的游戏、某档访谈节目"
                    />
                    {fields.series_title && (
                      <small className="field-error">{fields.series_title}</small>
                    )}
                  </label>
                  <div className="series-scope-editor field-wide">
                    <header>
                      <div>
                        <strong>分部、季度或季</strong>
                        <small>普通单季只填写起止集数；多部节目再添加一段。</small>
                      </div>
                      <button
                        className="button button-secondary button-small"
                        type="button"
                        onClick={() => {
                          const nextIndex = draft.series_scopes.length + 1;
                          const seriesScopes = [
                            ...draft.series_scopes,
                            {
                              key: `part-${nextIndex}`,
                              name: `第 ${nextIndex} 部`,
                              query: "",
                              episode_start: 1,
                              episode_end: 24
                            }
                          ];
                          setDraft({
                            ...draft,
                            series_scopes: seriesScopes,
                            rate_limit_requests: recommendedSeriesRequestBudget(seriesScopes)
                          });
                        }}
                      >
                        添加分部
                      </button>
                    </header>
                    <div className="series-scope-list">
                      {draft.series_scopes.map((scope, index) => (
                        <section className="series-scope-row" key={scope.key || index}>
                          <div className="series-scope-index" aria-hidden="true">
                            {index + 1}
                          </div>
                          <label className="field">
                            <span>范围名称（可选）</span>
                            <input
                              value={scope.name}
                              onChange={(event) =>
                                updateSeriesScope(index, { name: event.target.value })
                              }
                              placeholder="例如：第一部、第二季；单篇可留空"
                            />
                          </label>
                          <label className="field series-scope-query">
                            <span>节目别名（可选）</span>
                            <input
                              value={scope.query}
                              onChange={(event) =>
                                updateSeriesScope(index, { query: event.target.value })
                              }
                              placeholder="例如：MY FAIR PRINCESS II"
                            />
                          </label>
                          <label className="field">
                            <span>起始集</span>
                            <input
                              type="number"
                              min="1"
                              max="999"
                              value={scope.episode_start}
                              onChange={(event) =>
                                updateSeriesScope(index, {
                                  episode_start: Number(event.target.value)
                                })
                              }
                            />
                          </label>
                          <label className="field">
                            <span>最后一集</span>
                            <input
                              type="number"
                              min="1"
                              max="999"
                              value={scope.episode_end}
                              onChange={(event) =>
                                updateSeriesScope(index, {
                                  episode_end: Number(event.target.value)
                                })
                              }
                            />
                          </label>
                          <button
                            className="text-action text-danger series-scope-remove"
                            type="button"
                            disabled={draft.series_scopes.length === 1}
                            onClick={() => {
                              const seriesScopes = draft.series_scopes.filter(
                                (_, scopeIndex) => scopeIndex !== index
                              );
                              setDraft({
                                ...draft,
                                series_scopes: seriesScopes,
                                episode_start: Math.min(...seriesScopes.map((item) => item.episode_start)),
                                episode_end: Math.max(...seriesScopes.map((item) => item.episode_end)),
                                rate_limit_requests: recommendedSeriesRequestBudget(seriesScopes)
                              });
                            }}
                          >
                            删除
                          </button>
                          {fields[`series_scopes.${index}`] && (
                            <small className="field-error series-scope-error">
                              {fields[`series_scopes.${index}`]}
                            </small>
                          )}
                        </section>
                      ))}
                    </div>
                  {(fields.series_scopes || fields.episode_range) && (
                      <small className="field-error">
                        {fields.series_scopes || fields.episode_range}
                      </small>
                    )}
                  </div>
                  <label className="field field-wide">
                    <span>人物或识别词（可选）</span>
                    <textarea
                      rows={2}
                      value={listText.include_keywords}
                      onChange={(event) =>
                        setListText({ ...listText, include_keywords: event.target.value })
                      }
                      placeholder="例如：张哲瀚、龚俊；支持顿号、逗号、分号或换行"
                    />
                    <small>
                      任意命中一个即可。它只用于确认候选，不必重复填写节目名称。
                    </small>
                  </label>
                </>
              )}
              {draft.monitor_type !== "series" && (
                <label className="field field-wide">
                  <span>{draft.monitor_type === "channel" ? "频道内搜索词（可选）" : "搜索词"}</span>
                  <input
                    value={draft.query}
                    onChange={(event) => setDraft({ ...draft, query: event.target.value })}
                    aria-invalid={Boolean(fields.query)}
                    placeholder={draft.monitor_type === "channel" ? "留空表示监控频道全部内容" : "例如：OpenAI 新品发布"}
                  />
                  {fields.query && <small className="field-error">{fields.query}</small>}
                </label>
              )}
              {draft.monitor_type === "channel" && (
                <label className="field field-wide">
                  <span>频道主页链接、@账号或频道 ID（每行一个）</span>
                  <textarea
                    rows={4}
                    value={listText.channel_ids}
                    onChange={(event) =>
                      setListText({ ...listText, channel_ids: event.target.value })
                    }
                    aria-invalid={Boolean(fields.channel_ids)}
                    placeholder={"https://www.youtube.com/@handle\n或 @handle\n或 UC..."}
                  />
                  <small>直接从浏览器复制频道主页地址即可，无需查找技术 ID。</small>
                  {fields.channel_ids && (
                    <small className="field-error">{fields.channel_ids}</small>
                  )}
                </label>
              )}
            </div>
          </FieldGroup>

          <details
            className="monitor-advanced"
            open={advancedOpen}
            onToggle={(event) => setAdvancedOpen(event.currentTarget.open)}
          >
            <summary>
              <span className="monitor-advanced-mark"><Icon name="sliders" /></span>
              <span>
                <strong>高级筛选与请求设置</strong>
                <small>地区、分类、排除词、质量门槛和请求上限；通常保持默认即可。</small>
              </span>
              <span className="monitor-advanced-action">
                {advancedOpen ? "收起" : "展开"}
              </span>
            </summary>
            <div className="monitor-advanced-body">
              {draft.monitor_type === "series" && (
                <section className="work-panel monitor-advanced-inline">
                  <label className="field">
                    <span>额外检索词（可选）</span>
                    <input
                      value={draft.query}
                      onChange={(event) => setDraft({ ...draft, query: event.target.value })}
                      placeholder="仅在节目名称无法准确检索时填写，例如官方英文名"
                    />
                    <small>不要重复填写人物识别词，否则会把检索范围缩得过窄。</small>
                  </label>
                </section>
              )}
              <FieldGroup
                icon="discovery"
                title="范围与关键词过滤"
                description="候选返回后，本地规则会再次执行包含、排除与频道黑名单。"
              >
            <div className="settings-form-grid">
              {draft.monitor_type !== "series" && (
                <label className="field field-wide">
                  <span>至少命中一个关键词</span>
                  <textarea
                    rows={3}
                    value={listText.include_keywords}
                    onChange={(event) =>
                      setListText({ ...listText, include_keywords: event.target.value })
                    }
                    placeholder="支持顿号、逗号、分号或换行"
                  />
                </label>
              )}
              <label className="field field-wide">
                <span>排除关键词</span>
                <textarea
                  rows={3}
                  value={listText.exclude_keywords}
                  onChange={(event) =>
                    setListText({ ...listText, exclude_keywords: event.target.value })
                  }
                />
              </label>
              <label className="field field-wide">
                <span>排除频道 ID</span>
                <textarea
                  rows={3}
                  value={listText.exclude_channel_ids}
                  onChange={(event) =>
                    setListText({ ...listText, exclude_channel_ids: event.target.value })
                  }
                />
              </label>
              <label className="field">
                <span>地区代码</span>
                <input
                  maxLength={2}
                  value={draft.region_code}
                  onChange={(event) =>
                    setDraft({ ...draft, region_code: event.target.value.toUpperCase() })
                  }
                  aria-invalid={Boolean(fields.region_code)}
                />
                {fields.region_code && (
                  <small className="field-error">{fields.region_code}</small>
                )}
              </label>
              <label className="field">
                <span>YouTube 视频分类</span>
                <select
                  value={draft.category_id}
                  onChange={(event) =>
                    setDraft({ ...draft, category_id: event.target.value })
                  }
                  disabled={youtubeCategories.isPending}
                >
                  <option value="">全部分类</option>
                  {youtubeCategories.data?.items.map((category) => (
                    <option value={category.id} key={category.id}>
                      {category.title} · {category.id}
                    </option>
                  ))}
                </select>
                <small>
                  {youtubeCategories.isError
                    ? message(
                        youtubeCategories.error,
                        "分类读取失败，可先检查 YouTube 数据源。"
                      )
                    : youtubeCategories.data?.items[0]?.provider === "fixture"
                      ? "当前为本地测试分类，不代表 Google 实时分类。"
                      : "按地区从 YouTube Data API 获取可分配分类。"}
                </small>
              </label>
              <label className="field">
                <span>回溯天数</span>
                <input
                  type="number"
                  min="1"
                  max="30"
                  disabled={draft.channel_mode === "historical" || draft.monitor_type === "series"}
                  value={draft.lookback_days}
                  onChange={(event) => setNumber("lookback_days", Number(event.target.value))}
                />
              </label>
              <label className="field">
                <span>最大结果数</span>
                <input
                  type="number"
                  min="1"
                  max="50"
                  disabled={draft.monitor_type === "series"}
                  value={draft.max_results}
                  onChange={(event) => setNumber("max_results", Number(event.target.value))}
                />
              </label>
              <label className="field">
                <span>排序</span>
                <select
                  value={draft.order_by}
                  onChange={(event) =>
                    setDraft({
                      ...draft,
                      order_by: event.target.value as YouTubeMonitorInput["order_by"]
                    })
                  }
                >
                  <option value="date">发布时间</option>
                  <option value="viewCount">播放量</option>
                  <option value="rating">评分</option>
                  <option value="relevance">相关度</option>
                </select>
              </label>
              <label className="field">
                <span>指定开始日期</span>
                <input
                  type="date"
                  value={draft.published_after ?? ""}
                  onChange={(event) =>
                    setDraft({ ...draft, published_after: event.target.value || undefined })
                  }
                />
              </label>
              <label className="field">
                <span>指定结束日期</span>
                <input
                  type="date"
                  value={draft.published_before ?? ""}
                  onChange={(event) =>
                    setDraft({ ...draft, published_before: event.target.value || undefined })
                  }
                />
              </label>
            </div>
              </FieldGroup>

              <FieldGroup
                icon="sliders"
                title="视频类型与质量门槛"
                description="类型、互动指标和时长会写入每条判定记录。"
              >
            <div className="monitor-check-grid">
              <CheckCard
                checked={selectedVideos.has("video")}
                label="常规视频"
                onChange={() => toggleVideo("video")}
              />
              <CheckCard
                checked={selectedVideos.has("short")}
                label="Shorts"
                note="竖屏短内容候选"
                onChange={() => toggleVideo("short")}
              />
              <CheckCard
                checked={selectedVideos.has("live")}
                label="直播 / 回放"
                onChange={() => toggleVideo("live")}
              />
            </div>
            {fields.video_types && (
              <p className="field-error">{fields.video_types}</p>
            )}
            <div className="threshold-grid">
              {(
                [
                  ["min_view_count", "最低播放量"],
                  ["min_like_count", "最低点赞数"],
                  ["min_comment_count", "最低评论数"],
                  ["min_duration_seconds", "最短时长（秒）"],
                  ["max_duration_seconds", "最长时长（秒，0 不限）"]
                ] as [keyof YouTubeMonitorInput, string][]
              ).map(([key, label]) => (
                <label className="field" key={key}>
                  <span>{label}</span>
                  <input
                    type="number"
                    min="0"
                    value={draft[key] as number}
                    onChange={(event) => setNumber(key, Number(event.target.value))}
                  />
                </label>
              ))}
            </div>
              </FieldGroup>
              <section className="work-panel monitor-advanced-inline">
                <label className="field">
                  <span>单次请求上限</span>
                  <input
                    type="number"
                    min="1"
                    max="250"
                    value={draft.rate_limit_requests}
                    onChange={(event) =>
                      setNumber("rate_limit_requests", Number(event.target.value))
                    }
                  />
                  {draft.monitor_type === "series" && (
                    <small>
                      当前 {seriesEpisodeCount(draft.series_scopes)} 集建议至少 {recommendedSeriesRequestBudget(draft.series_scopes)} 次；系统已随集数自动调整。
                    </small>
                  )}
                  {fields.rate_limit_requests && (
                    <small className="field-error">{fields.rate_limit_requests}</small>
                  )}
                </label>
              </section>
            </div>
          </details>

          <FieldGroup
            icon="history"
            title="什么时候运行"
            description="可以定时自动检查，也可以只在需要时手动运行。服务重启不会丢失计划。"
          >
            <div className="mode-selector">
              <label>
                <input
                  type="radio"
                  checked={draft.schedule_type === "automatic"}
                  onChange={() => setDraft({ ...draft, schedule_type: "automatic" })}
                />
                <span>
                  <strong>自动调度</strong>
                  <small>按固定分钟间隔运行并计算下一次执行时间。</small>
                </span>
              </label>
              <label>
                <input
                  type="radio"
                  checked={draft.schedule_type === "manual"}
                  onChange={() => setDraft({ ...draft, schedule_type: "manual" })}
                />
                <span>
                  <strong>仅手动</strong>
                  <small>只在列表中点击“立即执行”时运行。</small>
                </span>
              </label>
            </div>
            <div className="settings-form-grid">
              <label className="field">
                <span>每隔多少分钟检查一次</span>
                <input
                  type="number"
                  min="1"
                  max="43200"
                  disabled={draft.schedule_type === "manual"}
                  value={draft.schedule_interval_minutes}
                  onChange={(event) =>
                    setNumber("schedule_interval_minutes", Number(event.target.value))
                  }
                />
              </label>
              <CheckCard
                checked={draft.enabled}
                label="保存后启用"
                note="关闭时配置保留但不会被调度。"
                onChange={(enabled) => setDraft({ ...draft, enabled })}
              />
            </div>
          </FieldGroup>

          <FieldGroup
            icon="route"
            title="发现内容后怎么处理"
            description="可以先只保存发现记录，也可以直接交给下载、字幕、审核和投稿流水线。"
          >
            <div className="mode-selector monitor-action-selector">
              <label>
                <input
                  type="radio"
                  checked={!draft.auto_add_to_tasks}
                  onChange={() => setDraft({ ...draft, auto_add_to_tasks: false })}
                />
                <span>
                  <strong>只保存发现记录</strong>
                  <small>先在运行记录中核对，确认后可批量加入任务。</small>
                </span>
              </label>
              <label>
                <input
                  type="radio"
                  checked={draft.auto_add_to_tasks}
                  onChange={() => setDraft({ ...draft, auto_add_to_tasks: true })}
                />
                <span>
                  <strong>自动加入任务流水线</strong>
                  <small>筛选通过后自动创建任务，继续下载、处理和审核。</small>
                </span>
              </label>
            </div>
            {draft.auto_add_to_tasks && (
              <div className="monitor-task-options">
                <div className="monitor-check-grid">
              <CheckCard
                checked={selectedPlatforms.has("bilibili")}
                label="Bilibili"
                onChange={() => togglePlatform("bilibili")}
              />
              <CheckCard
                checked={selectedPlatforms.has("acfun")}
                label="AcFun"
                onChange={() => togglePlatform("acfun")}
              />
                </div>
            {fields["task_template.target_platforms"] && (
              <p className="field-error">
                {fields["task_template.target_platforms"]}
              </p>
            )}
            <div className="settings-form-grid">
              <label className="field field-wide">
                <span>投稿策略</span>
                <select
                  value={draft.task_template.posting_strategy_id ?? ""}
                  onChange={(event) => {
                    const strategy = strategies.data?.items.find(
                      (item) => item.id === event.target.value && item.enabled
                    );
                    setDraft({
                      ...draft,
                      task_template: {
                        ...draft.task_template,
                        posting_strategy_id: event.target.value || undefined,
                        auto_publish:
                          strategy?.automation_mode === "automatic_after_review"
                            ? draft.task_template.auto_publish
                            : false,
                        ...(strategy
                          ? {
                              target_platforms: strategy.target_platforms,
                              repost_statement_version:
                                strategy.repost_statement_version
                            }
                          : {})
                      }
                    });
                  }}
                >
                  <option value="">不使用策略，审核后人工配置投稿</option>
                  {strategies.data?.items
                    .filter((strategy) => strategy.enabled)
                    .map((strategy) => (
                      <option value={strategy.id} key={strategy.id}>
                        {strategy.name} ·{" "}
                        {strategy.automation_mode === "automatic_after_review"
                          ? "审核后自动投稿"
                          : "审核后人工确认"}
                      </option>
                    ))}
                </select>
                {fields["task_template.posting_strategy_id"] && (
                  <small className="field-error">
                    {fields["task_template.posting_strategy_id"]}
                  </small>
                )}
              </label>
              <label className="field">
                <span>下载 Cookie</span>
                <select
                  value={draft.task_template.cookie_profile_id ?? ""}
                  onChange={(event) =>
                    setDraft({
                      ...draft,
                      task_template: {
                        ...draft.task_template,
                        cookie_profile_id: event.target.value || undefined
                      }
                    })
                  }
                >
                  <option value="">不使用 Cookie</option>
                  {cookies.data?.items.map((profile) => (
                    <option value={profile.id} key={profile.id}>
                      {profile.name} · {statusLabel(profile.status)}
                    </option>
                  ))}
                </select>
              </label>
              <label className="field">
                <span>转载声明</span>
                <select
                  value={draft.task_template.repost_statement_version}
                  onChange={(event) =>
                    setDraft({
                      ...draft,
                      task_template: {
                        ...draft.task_template,
                        repost_statement_version: event.target.value as
                          | "brief_v1"
                          | "full_v1"
                      }
                    })
                  }
                >
                  <option value="brief_v1">简版声明</option>
                  <option value="full_v1">完整版声明</option>
                </select>
                <small>声明只是信息披露，不代表获得版权许可。</small>
              </label>
            </div>
            <CheckCard
              checked={draft.task_template.auto_publish}
              disabled={
                selectedPostingStrategy?.automation_mode !==
                "automatic_after_review"
              }
              label="审核通过后自动投稿"
              note="必须选择“审核后自动投稿”策略；命中、下载、字幕、转码、审核和发布会组成完整自动化链路。"
              onChange={(auto_publish) =>
                setDraft({
                  ...draft,
                  task_template: {
                    ...draft.task_template,
                    auto_publish
                  }
                })
              }
            />
            {fields["task_template.auto_publish"] && (
              <p className="field-error">
                {fields["task_template.auto_publish"]}
              </p>
            )}
              </div>
            )}
          </FieldGroup>
        </div>

        <aside className="monitor-form-summary">
          <section className="work-panel">
            <p className="eyebrow">配置预览</p>
            <h2>{draft.name || "未命名监控"}</h2>
            <dl>
              <div>
                <dt>发现方式</dt>
                <dd>
                  {draft.monitor_type === "channel"
                    ? "指定频道"
                    : draft.monitor_type === "series"
                      ? "逐集检索"
                      : "关键词搜索"}
                </dd>
              </div>
              {draft.monitor_type === "series" && (
                <div>
                  <dt>覆盖范围</dt>
                  <dd>
                    {draft.series_scopes.length} 个篇章 · {totalSeriesEpisodes} 集
                  </dd>
                </div>
              )}
              {draft.monitor_type !== "series" && (
                <div>
                  <dt>候选上限</dt>
                  <dd>{draft.max_results} 条</dd>
                </div>
              )}
              <div>
                <dt>媒体类型</dt>
                <dd>{draft.video_types.map((kind) => ({ video: "常规视频", short: "短视频", live: "直播 / 回放" }[kind] ?? kind)).join(" / ") || "未选择"}</dd>
              </div>
              <div>
                <dt>运行方式</dt>
                <dd>
                  {draft.schedule_type === "automatic"
                    ? scheduleIntervalLabel(draft.schedule_interval_minutes)
                    : "手动"}
                </dd>
              </div>
              <div>
                <dt>命中处理</dt>
                <dd>
                  {draft.auto_add_to_tasks
                    ? draft.task_template.auto_publish
                      ? "建单并全自动投稿"
                      : "自动建单"
                    : "只留记录"}
                </dd>
              </div>
            </dl>
            <div className={`monitor-readiness ${readinessIssues.length === 0 ? "is-ready" : ""}`}>
              <strong>{readinessIssues.length === 0 ? "配置已具备保存条件" : "保存前还需处理"}</strong>
              {readinessIssues.length > 0 && (
                <ul>
                  {readinessIssues.map((item) => <li key={item}>{item}</li>)}
                </ul>
              )}
            </div>
            <p className="monitor-summary-note">
              同一 YouTube 视频在全局范围只会创建一条任务；并发监控命中时由数据库预留记录去重。
            </p>
          </section>
        </aside>
      </div>
    </>
  );
}
