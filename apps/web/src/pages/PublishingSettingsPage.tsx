import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import {
  api,
  ApiError,
  type Platform,
  type PlatformAccount,
  type PostingStrategy,
  type PostingStrategyInput,
  type TranscodePreset,
  type TranscodePresetInput
} from "../api";
import {
  LoadingBlock,
  PageHeader,
  PlatformChips,
  QueryError,
  StatusBadge
} from "../components";
import { formatDateTime, friendlyErrorMessage, statusLabel } from "../format";
import { TransientNotice } from "../product-ui";

type Tab = "accounts" | "categories" | "presets" | "strategies";

const platformLabel: Record<Platform, string> = {
  acfun: "AcFun",
  bilibili: "Bilibili"
};

const initialPreset: TranscodePresetInput = {
  name: "",
  enabled: true,
  encoder_mode: "auto",
  video_codec: "h264",
  audio_codec: "aac",
  container: "mp4",
  cpu_preset: "medium",
  high_resolution_cpu_preset: "fast",
  maximum_height: 1080,
  video_bitrate_kbps: 6000,
  audio_bitrate_kbps: 192,
  burn_subtitles: false,
  custom_arguments: []
};

const initialStrategy: PostingStrategyInput = {
  name: "",
  enabled: true,
  automation_mode: "manual_after_review",
  target_platforms: ["bilibili"],
  account_bindings: {},
  category_bindings: {},
  title_templates: {},
  description_templates: {},
  default_tags: [],
  repost_statement_version: "full_v1",
  require_content_moderation: false,
  schedule_mode: "immediate"
};

function messageOf(error: unknown, fallback: string) {
  return error instanceof ApiError ? error.message : fallback;
}

function AccountRow({
  account,
  cookies,
  onChanged,
  onNotice
}: {
  account: PlatformAccount;
  cookies: { id: string; name: string; status: string }[];
  onChanged: () => Promise<void>;
  onNotice: (message: string) => void;
}) {
  const [name, setName] = useState(account.name);
  const [cookieProfileId, setCookieProfileId] = useState(
    account.cookie_profile_id ?? ""
  );

  useEffect(() => {
    setName(account.name);
    setCookieProfileId(account.cookie_profile_id ?? "");
  }, [account.name, account.cookie_profile_id, account.version]);

  const update = useMutation({
    mutationFn: () =>
      api.updatePlatformAccount(account.id, {
        expected_version: account.version,
        name: name.trim(),
        cookie_profile_id: cookieProfileId || undefined
      }),
    onSuccess: async () => {
      onNotice(`${platformLabel[account.platform]} 账号配置已更新，需要重新校验。`);
      await onChanged();
    },
    onError: (error) => onNotice(messageOf(error, "账号更新失败"))
  });
  const check = useMutation({
    mutationFn: () => api.checkPlatformAccount(account.id),
    onSuccess: async (result) => {
      onNotice(result.message);
      await onChanged();
    },
    onError: (error) => onNotice(messageOf(error, "账号校验失败"))
  });
  const archive = useMutation({
    mutationFn: () => api.archivePlatformAccount(account.id, account.version),
    onSuccess: async () => {
      onNotice("账号已归档；历史投稿仍保留原账号记录。");
      await onChanged();
    },
    onError: (error) => onNotice(messageOf(error, "账号归档失败"))
  });
  const busy = update.isPending || check.isPending || archive.isPending;

  return (
    <article className="resource-card">
      <header>
        <div>
          <PlatformChips platforms={[account.platform]} />
          <h3>{account.name}</h3>
          <p>
            {account.auth_mode === "fixture"
              ? "本地测试账号 · 不会向平台投稿"
              : "Cookie 认证 · 真实平台账号"}
            {account.remote_display_name
              ? ` · ${account.remote_display_name}`
              : ""}
          </p>
        </div>
        <StatusBadge status={account.status} />
      </header>

      {account.auth_mode === "cookie" && (
        <div className="resource-inline-form">
          <label className="field">
            <span>账号名称</span>
            <input
              value={name}
              disabled={busy}
              onChange={(event) => setName(event.target.value)}
            />
          </label>
          <label className="field">
            <span>Cookie 配置</span>
            <select
              value={cookieProfileId}
              disabled={busy}
              onChange={(event) => setCookieProfileId(event.target.value)}
            >
              <option value="">请选择可用 Cookie</option>
              {cookies.map((cookie) => (
                <option value={cookie.id} key={cookie.id}>
                  {cookie.name} · {statusLabel(cookie.status)}
                </option>
              ))}
            </select>
          </label>
        </div>
      )}

      <dl className="resource-meta">
        <div>
          <dt>平台账号 ID</dt>
          <dd>{account.remote_user_id || "尚未校验"}</dd>
        </div>
        <div>
          <dt>连接方式</dt>
          <dd>{account.adapter_version ? "平台登录" : "等待校验"}</dd>
        </div>
        <div>
          <dt>上次校验</dt>
          <dd>
            {account.last_checked_at
              ? formatDateTime(account.last_checked_at)
              : "从未校验"}
          </dd>
        </div>
      </dl>

      {account.last_error_message && (
        <p className="resource-error">
          {friendlyErrorMessage(account.last_error_message)}
        </p>
      )}

      <footer>
        {account.auth_mode === "cookie" && (
          <button
            className="button button-secondary"
            type="button"
            disabled={
              busy ||
              !name.trim() ||
              !cookieProfileId ||
              (name === account.name &&
                cookieProfileId === account.cookie_profile_id)
            }
            onClick={() => update.mutate()}
          >
            {update.isPending ? "正在保存…" : "保存账号"}
          </button>
        )}
        <button
          className="button button-primary"
          type="button"
          disabled={busy}
          onClick={() => check.mutate()}
        >
          {check.isPending ? "正在校验…" : "校验登录"}
        </button>
        <button
          className="button button-quiet-danger"
          type="button"
          disabled={busy}
          onClick={() => {
            if (window.confirm(`确认归档账号“${account.name}”？`)) {
              archive.mutate();
            }
          }}
        >
          归档
        </button>
      </footer>
    </article>
  );
}

export default function PublishingSettingsPage() {
  const queryClient = useQueryClient();
  const [tab, setTab] = useState<Tab>("accounts");
  const [notice, setNotice] = useState("");
  const [accountPlatform, setAccountPlatform] = useState<Platform>("bilibili");
  const [accountName, setAccountName] = useState("");
  const [accountAuthMode, setAccountAuthMode] = useState<"cookie" | "fixture">(
    "cookie"
  );
  const [accountCookie, setAccountCookie] = useState("");
  const [categoryPlatform, setCategoryPlatform] =
    useState<Platform>("bilibili");
  const [refreshAccount, setRefreshAccount] = useState("");
  const [presetDraft, setPresetDraft] =
    useState<TranscodePresetInput>(initialPreset);
  const [editingPreset, setEditingPreset] = useState<TranscodePreset>();
  const [presetArguments, setPresetArguments] = useState("");
  const [strategyDraft, setStrategyDraft] =
    useState<PostingStrategyInput>(initialStrategy);
  const [editingStrategy, setEditingStrategy] = useState<PostingStrategy>();
  const [strategyTags, setStrategyTags] = useState("");

  const accounts = useQuery({
    queryKey: ["platform-accounts"],
    queryFn: () => api.platformAccounts()
  });
  const cookies = useQuery({
    queryKey: ["cookie-profiles"],
    queryFn: api.cookieProfiles
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
  const presets = useQuery({
    queryKey: ["transcode-presets"],
    queryFn: api.transcodePresets
  });
  const strategies = useQuery({
    queryKey: ["posting-strategies"],
    queryFn: api.postingStrategies
  });

  const refreshResources = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["platform-accounts"] }),
      queryClient.invalidateQueries({ queryKey: ["platform-categories"] }),
      queryClient.invalidateQueries({ queryKey: ["transcode-presets"] }),
      queryClient.invalidateQueries({ queryKey: ["posting-strategies"] })
    ]);
  };

  const createAccount = useMutation({
    mutationFn: () =>
      api.createPlatformAccount({
        platform: accountPlatform,
        name: accountName.trim(),
        auth_mode: accountAuthMode,
        ...(accountAuthMode === "cookie" && accountCookie
          ? { cookie_profile_id: accountCookie }
          : {})
      }),
    onSuccess: async () => {
      setNotice("投稿账号已创建，请执行登录校验后再用于投稿策略。");
      setAccountName("");
      setAccountCookie("");
      await refreshResources();
    },
    onError: (error) => setNotice(messageOf(error, "账号创建失败"))
  });

  const refreshCategories = useMutation({
    mutationFn: () => api.refreshPlatformCategories(refreshAccount),
    onSuccess: async (result) => {
      setNotice(`已刷新 ${result.items.length} 个平台分区。`);
      await queryClient.invalidateQueries({ queryKey: ["platform-categories"] });
    },
    onError: (error) => setNotice(messageOf(error, "分区刷新失败"))
  });

  const savePreset = useMutation({
    mutationFn: () => {
      const input: TranscodePresetInput = {
        ...presetDraft,
        custom_arguments: presetArguments
          .split("\n")
          .map((value) => value.trim())
          .filter(Boolean)
      };
      return editingPreset
        ? api.updateTranscodePreset(editingPreset.id, {
            ...input,
            expected_version: editingPreset.version
          })
        : api.createTranscodePreset(input);
    },
    onSuccess: async () => {
      setNotice(editingPreset ? "转码预设已更新。" : "转码预设已创建。");
      setEditingPreset(undefined);
      setPresetDraft(initialPreset);
      setPresetArguments("");
      await refreshResources();
    },
    onError: (error) => setNotice(messageOf(error, "转码预设保存失败"))
  });
  const archivePreset = useMutation({
    mutationFn: (preset: TranscodePreset) =>
      api.archiveTranscodePreset(preset.id, preset.version),
    onSuccess: async () => {
      setNotice("转码预设已归档；已创建任务继续使用冻结快照。");
      await refreshResources();
    },
    onError: (error) => setNotice(messageOf(error, "转码预设归档失败"))
  });

  const saveStrategy = useMutation({
    mutationFn: () => {
      const input: PostingStrategyInput = {
        ...strategyDraft,
        default_tags: strategyTags
          .split(/[,，\n]/)
          .map((value) => value.trim())
          .filter(Boolean),
        schedule_time:
          strategyDraft.schedule_mode === "daily_time"
            ? strategyDraft.schedule_time
            : undefined,
        transcode_preset_id: strategyDraft.transcode_preset_id || undefined
      };
      return editingStrategy
        ? api.updatePostingStrategy(editingStrategy.id, {
            ...input,
            expected_version: editingStrategy.version
          })
        : api.createPostingStrategy(input);
    },
    onSuccess: async () => {
      setNotice(editingStrategy ? "投稿策略已更新。" : "投稿策略已创建。");
      setEditingStrategy(undefined);
      setStrategyDraft(initialStrategy);
      setStrategyTags("");
      await refreshResources();
    },
    onError: (error) => setNotice(messageOf(error, "投稿策略保存失败"))
  });
  const archiveStrategy = useMutation({
    mutationFn: (strategy: PostingStrategy) =>
      api.archivePostingStrategy(strategy.id, strategy.version),
    onSuccess: async () => {
      setNotice("投稿策略已归档；历史任务继续使用创建时快照。");
      await refreshResources();
    },
    onError: (error) => setNotice(messageOf(error, "投稿策略归档失败"))
  });

  const readyAccounts = useMemo(
    () => (accounts.data?.items ?? []).filter((account) => account.status === "ready"),
    [accounts.data]
  );
  const activeCategories = useMemo(
    () => (categories.data?.items ?? []).filter((category) => category.active),
    [categories.data]
  );

  if (
    accounts.isPending ||
    cookies.isPending ||
    categories.isPending ||
    presets.isPending ||
    strategies.isPending
  ) {
    return <LoadingBlock label="正在读取平台投稿配置" />;
  }
  const firstError =
    accounts.error ??
    cookies.error ??
    categories.error ??
    presets.error ??
    strategies.error;
  if (firstError) {
    return (
      <QueryError
        title="无法读取投稿配置"
        message={messageOf(firstError, "投稿配置服务不可用")}
        retry={() => void refreshResources()}
      />
    );
  }

  const toggleStrategyPlatform = (platform: Platform) => {
    const selected = strategyDraft.target_platforms.includes(platform);
    setStrategyDraft({
      ...strategyDraft,
      target_platforms: selected
        ? strategyDraft.target_platforms.filter((item) => item !== platform)
        : [...strategyDraft.target_platforms, platform]
    });
  };

  return (
    <>
      <PageHeader
        title="平台投稿配置"
        description="管理 AcFun / Bilibili 登录账号、平台分区、媒体规格和审核后的投稿策略。"
        actions={
          <div className="review-heading-actions">
            <Link className="button button-secondary" to="/publishing">
              返回投稿队列
            </Link>
            <Link
              className="button button-secondary"
              to="/settings?section=publishing"
            >
              发布运行参数
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

      <nav className="resource-tabs" aria-label="投稿配置分类">
        {(
          [
            ["accounts", "平台账号", `${accounts.data?.items.length ?? 0} 个`],
            ["categories", "视频分区", `${activeCategories.length} 个`],
            ["presets", "转码预设", `${presets.data?.items.length ?? 0} 个`],
            ["strategies", "投稿策略", `${strategies.data?.items.length ?? 0} 个`]
          ] as [Tab, string, string][]
        ).map(([key, label, count]) => (
          <button
            type="button"
            className={tab === key ? "is-active" : ""}
            aria-current={tab === key ? "page" : undefined}
            onClick={() => setTab(key)}
            key={key}
          >
            <strong>{label}</strong>
            <span>{count}</span>
          </button>
        ))}
      </nav>

      {tab === "accounts" && (
        <div className="resource-layout">
          <section className="work-panel resource-form-panel">
            <header>
              <p className="eyebrow">新增账号</p>
              <h2>绑定平台登录</h2>
              <p>生产账号使用已同步的 Cookie；本地测试账号不会访问真实平台。</p>
            </header>
            <div className="settings-form-grid">
              <label className="field">
                <span>平台</span>
                <select
                  value={accountPlatform}
                  onChange={(event) =>
                    setAccountPlatform(event.target.value as Platform)
                  }
                >
                  <option value="bilibili">Bilibili</option>
                  <option value="acfun">AcFun</option>
                </select>
              </label>
              <label className="field">
                <span>账号名称</span>
                <input
                  value={accountName}
                  onChange={(event) => setAccountName(event.target.value)}
                  placeholder="例如：主站运营号"
                />
              </label>
              <label className="field">
                <span>认证方式</span>
                <select
                  value={accountAuthMode}
                  onChange={(event) => {
                    setAccountAuthMode(
                      event.target.value as "cookie" | "fixture"
                    );
                    setAccountCookie("");
                  }}
                >
                  <option value="cookie">真实平台 · Cookie 认证</option>
                  <option value="fixture">仅本地测试 · 不会投稿</option>
                </select>
              </label>
              {accountAuthMode === "cookie" && (
                <label className="field">
                  <span>Cookie 配置</span>
                  <select
                    value={accountCookie}
                    onChange={(event) => setAccountCookie(event.target.value)}
                  >
                    <option value="">请选择已同步配置</option>
                    {cookies.data?.items
                      .filter((cookie) => cookie.has_usable_cookies)
                      .map((cookie) => (
                        <option value={cookie.id} key={cookie.id}>
                          {cookie.name} · {cookie.cookie_count} 条
                        </option>
                      ))}
                  </select>
                </label>
              )}
            </div>
            <button
              className="button button-primary"
              type="button"
              disabled={
                createAccount.isPending ||
                !accountName.trim() ||
                (accountAuthMode === "cookie" && !accountCookie)
              }
              onClick={() => createAccount.mutate()}
            >
              {createAccount.isPending ? "正在创建…" : "创建投稿账号"}
            </button>
          </section>

          <div className="resource-card-list">
            {accounts.data?.items.map((account) => (
              <AccountRow
                account={account}
                cookies={cookies.data?.items ?? []}
                onChanged={refreshResources}
                onNotice={setNotice}
                key={account.id}
              />
            ))}
          </div>
        </div>
      )}

      {tab === "categories" && (
        <div className="resource-layout">
          <section className="work-panel resource-form-panel">
            <header>
              <p className="eyebrow">平台分区</p>
              <h2>从真实账号刷新</h2>
              <p>分区从目标平台同步并记录刷新时间；投稿草稿会保存平台原始分区 ID。</p>
            </header>
            <label className="field">
              <span>已校验账号</span>
              <select
                value={refreshAccount}
                onChange={(event) => {
                  setRefreshAccount(event.target.value);
                  const account = readyAccounts.find(
                    (item) => item.id === event.target.value
                  );
                  if (account) setCategoryPlatform(account.platform);
                }}
              >
                <option value="">选择账号</option>
                {readyAccounts.map((account) => (
                  <option value={account.id} key={account.id}>
                    {platformLabel[account.platform]} · {account.name}
                  </option>
                ))}
              </select>
            </label>
            <button
              className="button button-primary"
              type="button"
              disabled={!refreshAccount || refreshCategories.isPending}
              onClick={() => refreshCategories.mutate()}
            >
              {refreshCategories.isPending ? "正在刷新…" : "刷新平台分区"}
            </button>
          </section>

          <section className="work-panel resource-table-panel">
            <header className="resource-table-head">
              <div>
                <h2>{platformLabel[categoryPlatform]} 视频分区</h2>
                <p>仅展示当前有效分区。</p>
              </div>
              <div className="segmented-control">
                {(["bilibili", "acfun"] as Platform[]).map((platform) => (
                  <button
                    type="button"
                    className={categoryPlatform === platform ? "is-active" : ""}
                    onClick={() => setCategoryPlatform(platform)}
                    key={platform}
                  >
                    {platformLabel[platform]}
                  </button>
                ))}
              </div>
            </header>
            <div className="resource-table-scroll">
              <table className="resource-table">
                <thead>
                  <tr>
                    <th>分区路径</th>
                    <th>分区 ID</th>
                    <th>刷新时间</th>
                  </tr>
                </thead>
                <tbody>
                  {activeCategories
                    .filter((category) => category.platform === categoryPlatform)
                    .map((category) => (
                      <tr key={`${category.platform}-${category.category_id}`}>
                        <td>{category.path || category.name}</td>
                        <td>{category.category_id}</td>
                        <td>{formatDateTime(category.refreshed_at)}</td>
                      </tr>
                    ))}
                </tbody>
              </table>
            </div>
          </section>
        </div>
      )}

      {tab === "presets" && (
        <div className="resource-layout">
          <section className="work-panel resource-form-panel">
            <header>
              <p className="eyebrow">
                {editingPreset ? "编辑预设" : "新增预设"}
              </p>
              <h2>{editingPreset?.name ?? "平台媒体规格"}</h2>
              <p>预设会随任务保存，后续编辑不会改变已经开始处理的任务。</p>
            </header>
            <div className="settings-form-grid">
              <label className="field field-wide">
                <span>名称</span>
                <input
                  value={presetDraft.name}
                  onChange={(event) =>
                    setPresetDraft({ ...presetDraft, name: event.target.value })
                  }
                />
              </label>
              <label className="field">
                <span>编码模式</span>
                <select
                  value={presetDraft.encoder_mode}
                  onChange={(event) =>
                    setPresetDraft({
                      ...presetDraft,
                      encoder_mode: event.target.value
                    })
                  }
                >
                  <option value="auto">自动</option>
                  <option value="cpu">CPU</option>
                  <option value="nvidia">NVIDIA</option>
                  <option value="intel">Intel</option>
                  <option value="amd">AMD</option>
                </select>
              </label>
              <label className="field">
                <span>视频编码</span>
                <select
                  value={presetDraft.video_codec}
                  onChange={(event) =>
                    setPresetDraft({
                      ...presetDraft,
                      video_codec: event.target.value
                    })
                  }
                >
                  <option value="h264">H.264</option>
                  <option value="hevc">HEVC（仅可用硬件路径）</option>
                  <option value="copy">不重编码</option>
                </select>
              </label>
              <label className="field">
                <span>音频编码</span>
                <select
                  value={presetDraft.audio_codec}
                  onChange={(event) =>
                    setPresetDraft({
                      ...presetDraft,
                      audio_codec: event.target.value
                    })
                  }
                >
                  <option value="aac">AAC</option>
                  <option value="copy">不重编码</option>
                </select>
              </label>
              <label className="field">
                <span>封装</span>
                <select
                  value={presetDraft.container}
                  onChange={(event) =>
                    setPresetDraft({
                      ...presetDraft,
                      container: event.target.value
                    })
                  }
                >
                  <option value="mp4">MP4</option>
                  <option value="mkv">MKV</option>
                </select>
              </label>
              <label className="field">
                <span>处理速度</span>
                <select
                  value={presetDraft.cpu_preset}
                  onChange={(event) =>
                    setPresetDraft({
                      ...presetDraft,
                      cpu_preset: event.target.value
                    })
                  }
                >
                  {[
                    "ultrafast",
                    "superfast",
                    "veryfast",
                    "faster",
                    "fast",
                    "medium",
                    "slow",
                    "slower",
                    "veryslow"
                  ].map((value) => (
                    <option value={value} key={value}>{value}</option>
                  ))}
                </select>
              </label>
              <label className="field">
                <span>高分辨率 preset</span>
                <select
                  value={presetDraft.high_resolution_cpu_preset}
                  onChange={(event) =>
                    setPresetDraft({
                      ...presetDraft,
                      high_resolution_cpu_preset: event.target.value
                    })
                  }
                >
                  {["veryfast", "faster", "fast", "medium", "slow"].map(
                    (value) => (
                      <option value={value} key={value}>{value}</option>
                    )
                  )}
                </select>
              </label>
              {(
                [
                  ["maximum_height", "最大高度"],
                  ["video_bitrate_kbps", "视频码率 Kbps"],
                  ["audio_bitrate_kbps", "音频码率 Kbps"]
                ] as const
              ).map(([key, label]) => (
                <label className="field" key={key}>
                  <span>{label}</span>
                  <input
                    type="number"
                    min="0"
                    value={presetDraft[key]}
                    onChange={(event) =>
                      setPresetDraft({
                        ...presetDraft,
                        [key]: Number(event.target.value)
                      })
                    }
                  />
                </label>
              ))}
              <label className="check-card">
                <input
                  type="checkbox"
                  checked={presetDraft.enabled}
                  onChange={(event) =>
                    setPresetDraft({
                      ...presetDraft,
                      enabled: event.target.checked
                    })
                  }
                />
                <span aria-hidden="true" />
                <div><strong>允许新任务使用</strong></div>
              </label>
              <label className="check-card">
                <input
                  type="checkbox"
                  checked={presetDraft.burn_subtitles}
                  onChange={(event) =>
                    setPresetDraft({
                      ...presetDraft,
                      burn_subtitles: event.target.checked
                    })
                  }
                />
                <span aria-hidden="true" />
                <div><strong>烧录字幕</strong></div>
              </label>
              <label className="field field-wide">
                <span>高级参数（每行一个参数或参数值）</span>
                <textarea
                  rows={6}
                  value={presetArguments}
                  onChange={(event) => setPresetArguments(event.target.value)}
                  placeholder={"-movflags\n+faststart"}
                />
              </label>
            </div>
            <div className="resource-form-actions">
              <button
                className="button button-primary"
                type="button"
                disabled={savePreset.isPending || !presetDraft.name.trim()}
                onClick={() => savePreset.mutate()}
              >
                {savePreset.isPending
                  ? "正在保存…"
                  : editingPreset
                    ? "保存预设版本"
                    : "创建转码预设"}
              </button>
              {editingPreset && (
                <button
                  className="button button-secondary"
                  type="button"
                  onClick={() => {
                    setEditingPreset(undefined);
                    setPresetDraft(initialPreset);
                    setPresetArguments("");
                  }}
                >
                  取消编辑
                </button>
              )}
            </div>
          </section>

          <div className="resource-card-list">
            {presets.data?.items.map((preset) => (
              <article className="resource-card" key={preset.id}>
                <header>
                  <div>
                    <h3>{preset.name}</h3>
                    <p>
                      {preset.video_codec.toUpperCase()} /{" "}
                      {preset.audio_codec.toUpperCase()} · {preset.maximum_height || "原始"}p
                    </p>
                  </div>
                  <StatusBadge status={preset.enabled ? "ready" : "disabled"} />
                </header>
                <dl className="resource-meta">
                  <div><dt>编码模式</dt><dd>{preset.encoder_mode}</dd></div>
                  <div><dt>封装</dt><dd>{preset.container}</dd></div>
                  <div><dt>字幕烧录</dt><dd>{preset.burn_subtitles ? "是" : "否"}</dd></div>
                </dl>
                <footer>
                  <button
                    className="button button-secondary"
                    type="button"
                    onClick={() => {
                      setEditingPreset(preset);
                      setPresetDraft({
                        name: preset.name,
                        enabled: preset.enabled,
                        encoder_mode: preset.encoder_mode,
                        video_codec: preset.video_codec,
                        audio_codec: preset.audio_codec,
                        container: preset.container,
                        cpu_preset: preset.cpu_preset,
                        high_resolution_cpu_preset:
                          preset.high_resolution_cpu_preset,
                        maximum_height: preset.maximum_height,
                        video_bitrate_kbps: preset.video_bitrate_kbps,
                        audio_bitrate_kbps: preset.audio_bitrate_kbps,
                        burn_subtitles: preset.burn_subtitles,
                        custom_arguments: preset.custom_arguments
                      });
                      setPresetArguments(preset.custom_arguments.join("\n"));
                      window.scrollTo({ top: 0, behavior: "smooth" });
                    }}
                  >
                    编辑
                  </button>
                  <button
                    className="button button-quiet-danger"
                    type="button"
                    disabled={archivePreset.isPending}
                    onClick={() => {
                      if (window.confirm(`确认归档预设“${preset.name}”？`)) {
                        archivePreset.mutate(preset);
                      }
                    }}
                  >
                    归档
                  </button>
                </footer>
              </article>
            ))}
          </div>
        </div>
      )}

      {tab === "strategies" && (
        <div className="resource-layout">
          <section className="work-panel resource-form-panel">
            <header>
              <p className="eyebrow">
                {editingStrategy ? "编辑策略" : "新增策略"}
              </p>
              <h2>{editingStrategy?.name ?? "审核后的投稿路径"}</h2>
              <p>账号、分区、模板和转码预设会冻结进任务，避免运行中配置漂移。</p>
            </header>
            <div className="settings-form-grid">
              <label className="field field-wide">
                <span>策略名称</span>
                <input
                  value={strategyDraft.name}
                  onChange={(event) =>
                    setStrategyDraft({
                      ...strategyDraft,
                      name: event.target.value
                    })
                  }
                />
              </label>
              <label className="field">
                <span>审核通过后</span>
                <select
                  value={strategyDraft.automation_mode}
                  onChange={(event) =>
                    setStrategyDraft({
                      ...strategyDraft,
                      automation_mode: event.target.value as
                        PostingStrategyInput["automation_mode"]
                    })
                  }
                >
                  <option value="manual_after_review">人工核对后入队</option>
                  <option value="automatic_after_review">自动加入发布队列</option>
                </select>
              </label>
              <label className="field">
                <span>转码预设</span>
                <select
                  value={strategyDraft.transcode_preset_id ?? ""}
                  onChange={(event) =>
                    setStrategyDraft({
                      ...strategyDraft,
                      transcode_preset_id: event.target.value || undefined
                    })
                  }
                >
                  <option value="">使用全局转码配置</option>
                  {presets.data?.items
                    .filter((preset) => preset.enabled)
                    .map((preset) => (
                      <option value={preset.id} key={preset.id}>
                        {preset.name}
                      </option>
                    ))}
                </select>
              </label>
              <label className="field">
                <span>转载声明</span>
                <select
                  value={strategyDraft.repost_statement_version}
                  onChange={(event) =>
                    setStrategyDraft({
                      ...strategyDraft,
                      repost_statement_version: event.target.value as
                        PostingStrategyInput["repost_statement_version"]
                    })
                  }
                >
                  <option value="brief_v1">简版</option>
                  <option value="full_v1">完整版</option>
                </select>
              </label>
              <label className="field">
                <span>发布时间</span>
                <select
                  value={strategyDraft.schedule_mode}
                  onChange={(event) =>
                    setStrategyDraft({
                      ...strategyDraft,
                      schedule_mode: event.target.value
                    })
                  }
                >
                  <option value="immediate">审核后立即</option>
                  <option value="daily_time">每日固定时间</option>
                </select>
              </label>
              {strategyDraft.schedule_mode === "daily_time" && (
                <label className="field">
                  <span>每日时间</span>
                  <input
                    type="time"
                    value={strategyDraft.schedule_time ?? ""}
                    onChange={(event) =>
                      setStrategyDraft({
                        ...strategyDraft,
                        schedule_time: event.target.value
                      })
                    }
                  />
                </label>
              )}
              <label className="field field-wide">
                <span>默认标签（逗号分隔）</span>
                <input
                  value={strategyTags}
                  onChange={(event) => setStrategyTags(event.target.value)}
                />
              </label>
              <label className="check-card">
                <input
                  type="checkbox"
                  checked={strategyDraft.enabled}
                  onChange={(event) =>
                    setStrategyDraft({
                      ...strategyDraft,
                      enabled: event.target.checked
                    })
                  }
                />
                <span aria-hidden="true" />
                <div><strong>允许新任务选择</strong></div>
              </label>
              <label className="check-card">
                <input
                  type="checkbox"
                  checked={strategyDraft.require_content_moderation}
                  onChange={(event) =>
                    setStrategyDraft({
                      ...strategyDraft,
                      require_content_moderation: event.target.checked
                    })
                  }
                />
                <span aria-hidden="true" />
                <div>
                  <strong>强制内容安全审核</strong>
                  <small>未启用内容安全时不能使用此策略建单。</small>
                </div>
              </label>
            </div>

            <fieldset className="strategy-platforms">
              <legend>目标平台与绑定</legend>
              {(["bilibili", "acfun"] as Platform[]).map((platform) => {
                const selected =
                  strategyDraft.target_platforms.includes(platform);
                return (
                  <section className={selected ? "is-selected" : ""} key={platform}>
                    <label className="check-card">
                      <input
                        type="checkbox"
                        checked={selected}
                        onChange={() => toggleStrategyPlatform(platform)}
                      />
                      <span aria-hidden="true" />
                      <div><strong>{platformLabel[platform]}</strong></div>
                    </label>
                    {selected && (
                      <div className="settings-form-grid">
                        <label className="field">
                          <span>投稿账号</span>
                          <select
                            value={
                              strategyDraft.account_bindings[platform] ?? ""
                            }
                            onChange={(event) =>
                              setStrategyDraft({
                                ...strategyDraft,
                                account_bindings: {
                                  ...strategyDraft.account_bindings,
                                  [platform]: event.target.value
                                }
                              })
                            }
                          >
                            <option value="">选择账号</option>
                            {(accounts.data?.items ?? [])
                              .filter((account) => account.platform === platform)
                              .map((account) => (
                                <option value={account.id} key={account.id}>
                                  {account.name} · {statusLabel(account.status)} ·{" "}
                                  {account.auth_mode === "fixture"
                                    ? "本地测试（不会投稿）"
                                    : "Cookie 认证"}
                                </option>
                              ))}
                          </select>
                        </label>
                        <label className="field">
                          <span>视频分区</span>
                          <select
                            value={
                              strategyDraft.category_bindings[platform] ?? ""
                            }
                            onChange={(event) =>
                              setStrategyDraft({
                                ...strategyDraft,
                                category_bindings: {
                                  ...strategyDraft.category_bindings,
                                  [platform]: event.target.value
                                }
                              })
                            }
                          >
                            <option value="">选择分区</option>
                            {activeCategories
                              .filter(
                                (category) => category.platform === platform
                              )
                              .map((category) => (
                                <option
                                  value={category.category_id}
                                  key={category.category_id}
                                >
                                  {category.path || category.name}
                                </option>
                              ))}
                          </select>
                        </label>
                        <label className="field field-wide">
                          <span>标题模板</span>
                          <input
                            value={
                              strategyDraft.title_templates[platform] ?? ""
                            }
                            onChange={(event) =>
                              setStrategyDraft({
                                ...strategyDraft,
                                title_templates: {
                                  ...strategyDraft.title_templates,
                                  [platform]: event.target.value
                                }
                              })
                            }
                            placeholder="{{title}}"
                          />
                        </label>
                        <label className="field field-wide">
                          <span>简介模板</span>
                          <textarea
                            rows={5}
                            value={
                              strategyDraft.description_templates[platform] ??
                              ""
                            }
                            onChange={(event) =>
                              setStrategyDraft({
                                ...strategyDraft,
                                description_templates: {
                                  ...strategyDraft.description_templates,
                                  [platform]: event.target.value
                                }
                              })
                            }
                            placeholder={"{{description}}\n\n{{repost_statement}}"}
                          />
                        </label>
                      </div>
                    )}
                  </section>
                );
              })}
            </fieldset>

            <div className="resource-form-actions">
              <button
                className="button button-primary"
                type="button"
                disabled={
                  saveStrategy.isPending ||
                  !strategyDraft.name.trim() ||
                  strategyDraft.target_platforms.length === 0
                }
                onClick={() => saveStrategy.mutate()}
              >
                {saveStrategy.isPending
                  ? "正在保存…"
                  : editingStrategy
                    ? "保存策略版本"
                    : "创建投稿策略"}
              </button>
              {editingStrategy && (
                <button
                  className="button button-secondary"
                  type="button"
                  onClick={() => {
                    setEditingStrategy(undefined);
                    setStrategyDraft(initialStrategy);
                    setStrategyTags("");
                  }}
                >
                  取消编辑
                </button>
              )}
            </div>
          </section>

          <div className="resource-card-list">
            {strategies.data?.items.map((strategy) => (
              <article className="resource-card" key={strategy.id}>
                <header>
                  <div>
                    <PlatformChips platforms={strategy.target_platforms} />
                    <h3>{strategy.name}</h3>
                    <p>
                      {strategy.automation_mode === "automatic_after_review"
                        ? "审核后自动投稿"
                        : "审核后人工确认"}
                    </p>
                  </div>
                  <StatusBadge status={strategy.enabled ? "ready" : "disabled"} />
                </header>
                <dl className="resource-meta">
                  <div>
                    <dt>内容安全</dt>
                    <dd>{strategy.require_content_moderation ? "强制" : "按全局"}</dd>
                  </div>
                  <div>
                    <dt>发布时间</dt>
                    <dd>
                      {strategy.schedule_mode === "daily_time"
                        ? `每日 ${strategy.schedule_time}`
                        : "立即"}
                    </dd>
                  </div>
                  <div>
                    <dt>转载声明</dt>
                    <dd>
                      {strategy.repost_statement_version === "full_v1"
                        ? "完整版"
                        : "简版"}
                    </dd>
                  </div>
                </dl>
                <footer>
                  <button
                    className="button button-secondary"
                    type="button"
                    onClick={() => {
                      setEditingStrategy(strategy);
                      setStrategyDraft({
                        name: strategy.name,
                        enabled: strategy.enabled,
                        automation_mode: strategy.automation_mode,
                        target_platforms: [...strategy.target_platforms],
                        account_bindings: { ...strategy.account_bindings },
                        category_bindings: { ...strategy.category_bindings },
                        title_templates: { ...strategy.title_templates },
                        description_templates: {
                          ...strategy.description_templates
                        },
                        default_tags: [...strategy.default_tags],
                        repost_statement_version:
                          strategy.repost_statement_version,
                        transcode_preset_id:
                          strategy.transcode_preset_id,
                        require_content_moderation:
                          strategy.require_content_moderation,
                        schedule_mode: strategy.schedule_mode,
                        schedule_time: strategy.schedule_time
                      });
                      setStrategyTags(strategy.default_tags.join(", "));
                      window.scrollTo({ top: 0, behavior: "smooth" });
                    }}
                  >
                    编辑
                  </button>
                  <button
                    className="button button-quiet-danger"
                    type="button"
                    disabled={archiveStrategy.isPending}
                    onClick={() => {
                      if (window.confirm(`确认归档策略“${strategy.name}”？`)) {
                        archiveStrategy.mutate(strategy);
                      }
                    }}
                  >
                    归档
                  </button>
                </footer>
              </article>
            ))}
          </div>
        </div>
      )}
    </>
  );
}
