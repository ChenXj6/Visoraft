import { useEffect, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import {
  api,
  ApiError,
  type ApplicationSettings,
  type ModelEndpoint,
  type PromptEntry,
  type UpdateSettingsInput
} from "../api";
import { LoadingBlock, PageHeader, QueryError } from "../components";
import { Icon, type IconName } from "../icons";
import {
  ExternalGuideLink,
  HelpLink,
  LanguageSelect,
  TransientNotice
} from "../product-ui";

type SettingsTab =
  | "review"
  | "automation"
  | "models"
  | "subtitles"
  | "prompts"
  | "transcode"
  | "moderation"
  | "publishing"
  | "youtube";
type ModelKey = keyof ApplicationSettings["models"];
type PromptKey = keyof ApplicationSettings["prompts"];

const secretKeys: Record<ModelKey, string> = {
  global: "model.global.api_key",
  subtitle_translation: "model.subtitle_translation.api_key",
  subtitle_qc: "model.subtitle_qc.api_key",
  smart_segmentation: "model.smart_segmentation.api_key"
};

const existingSubtitleSources = [
  [
    "inspect_platform_subtitles",
    "平台字幕",
    "优先复用 YouTube 提供的中文人工或自动字幕。"
  ],
  [
    "inspect_embedded_subtitles",
    "内嵌字幕轨",
    "读取媒体文件中的中文软字幕轨并提取使用。"
  ],
  [
    "inspect_hardcoded_subtitles",
    "画面硬字幕",
    "对下方字幕区域抽帧识别，排除单一水印与零散文字。"
  ]
] as const;

const tabLabels: Record<SettingsTab, { icon: IconName; label: string; note: string }> = {
  review: { icon: "review", label: "审核策略", note: "手动 / 自动" },
  automation: { icon: "route", label: "全流程", note: "处理自动化" },
  models: { icon: "model", label: "模型接入", note: "全局与覆盖" },
  subtitles: { icon: "subtitles", label: "字幕与语音", note: "识别 / 分段 / 质检" },
  prompts: { icon: "prompt", label: "提示词", note: "翻译与质检规则" },
  transcode: { icon: "media", label: "视频处理", note: "格式与画质" },
  moderation: { icon: "shield", label: "内容安全", note: "阿里云凭证" },
  publishing: { icon: "route", label: "投稿运行", note: "并发 / 重试" },
  youtube: { icon: "monitor", label: "YouTube", note: "监控数据源" }
};

function message(error: unknown, fallback: string) {
  return error instanceof ApiError ? error.message : fallback;
}

function languageHintMatches(sourceLanguage: string, languageHints: string) {
  const base = (value: string) =>
    value.trim().toLowerCase().replace("_", "-").split("-")[0];
  const source = base(sourceLanguage);
  const hints = languageHints.trim();
  if (!source || source === "auto" || !hints || hints.toLowerCase() === "auto") {
    return true;
  }
  return hints.split(",").some((hint) => base(hint) === source);
}

function Toggle({
  checked,
  onChange,
  label,
  description
}: {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label: string;
  description?: string;
}) {
  return (
    <label className="ops-toggle">
      <input
        type="checkbox"
        checked={checked}
        onChange={(event) => onChange(event.target.checked)}
      />
      <span className="ops-toggle-control" aria-hidden="true" />
      <span>
        <strong>{label}</strong>
        {description && <small>{description}</small>}
      </span>
    </label>
  );
}

function SettingsSection({
  icon,
  title,
  description,
  help,
  children
}: {
  icon: IconName;
  title: string;
  description: string;
  help?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="work-panel settings-section">
      <header className="section-heading">
        <span className="sequence-mark"><Icon name={icon} /></span>
        <div className="section-heading-copy">
          <div className="section-heading-tools">
            <h2>{title}</h2>
            {help}
          </div>
          <p>{description}</p>
        </div>
      </header>
      {children}
    </section>
  );
}

function ModelEditor({
  modelKey,
  title,
  description,
  endpoint,
  configured,
  secret,
  onEndpoint,
  onSecret,
  onTest,
  testing,
  testNotice
}: {
  modelKey: ModelKey;
  title: string;
  description: string;
  endpoint: ModelEndpoint;
  configured: boolean;
  secret: string;
  onEndpoint: (next: ModelEndpoint) => void;
  onSecret: (value: string) => void;
  onTest: () => void;
  testing: boolean;
  testNotice?: string;
}) {
  const global = modelKey === "global";
  const active = global ? Boolean(endpoint.enabled) : endpoint.mode === "override";
  return (
    <article className="config-slab">
      <header>
        <div>
          <p className="eyebrow">{global ? "全局默认" : "专用覆盖"}</p>
          <h3>{title}</h3>
          <p>{description}</p>
        </div>
        <span className={`config-readiness ${active ? "is-on" : ""}`}>
          {active
            ? "已启用"
            : global
              ? "未启用"
              : endpoint.mode === "inherit"
                ? "继承全局"
                : "未启用"}
        </span>
      </header>

      {global ? (
        <Toggle
          checked={Boolean(endpoint.enabled)}
          onChange={(enabled) => onEndpoint({ ...endpoint, enabled })}
          label="启用全局模型"
          description="元数据、翻译及未配置专用覆盖的能力从这里继承。"
        />
      ) : (
        <label className="field">
          <span>使用策略</span>
          <select
            value={endpoint.mode ?? "inherit"}
            onChange={(event) =>
              onEndpoint({
                ...endpoint,
                mode: event.target.value as ModelEndpoint["mode"]
              })
            }
          >
            <option value="inherit">继承全局模型</option>
            <option value="override">使用专用覆盖</option>
            <option value="disabled">禁用此能力的模型调用</option>
          </select>
        </label>
      )}

      {(global || endpoint.mode === "override") && (
        <div className="settings-form-grid">
          <label className="field">
            <span>协议</span>
            <select
              value={endpoint.provider}
              onChange={(event) =>
                onEndpoint({
                  ...endpoint,
                  provider: event.target.value as ModelEndpoint["provider"]
                })
              }
            >
              <option value="openai_compatible">OpenAI 兼容</option>
              <option value="fixture">演示模式（不连接外部服务）</option>
            </select>
          </label>
          <label className="field field-wide">
            <span>Base URL</span>
            <input
              type="url"
              value={endpoint.base_url}
              onChange={(event) =>
                onEndpoint({ ...endpoint, base_url: event.target.value })
              }
              placeholder="https://api.example.com/v1"
            />
          </label>
          <label className="field">
            <span>模型</span>
            <input
              value={endpoint.model}
              onChange={(event) => onEndpoint({ ...endpoint, model: event.target.value })}
              placeholder="model-name"
            />
          </label>
          <label className="field field-wide">
            <span>API Key</span>
            <input
              type="password"
              value={secret}
              onChange={(event) => onSecret(event.target.value)}
              placeholder={configured ? "已配置；留空保持不变" : "尚未配置"}
              autoComplete="new-password"
            />
          </label>
          <label className="field">
            <span>温度</span>
            <input
              type="number"
              min="0"
              max="2"
              step="0.1"
              value={endpoint.temperature}
              onChange={(event) =>
                onEndpoint({ ...endpoint, temperature: Number(event.target.value) })
              }
            />
          </label>
          <label className="field">
            <span>超时（秒）</span>
            <input
              type="number"
              min="5"
              max="600"
              value={endpoint.timeout_seconds}
              onChange={(event) =>
                onEndpoint({ ...endpoint, timeout_seconds: Number(event.target.value) })
              }
            />
          </label>
          <Toggle
            checked={endpoint.thinking}
            onChange={(thinking) => onEndpoint({ ...endpoint, thinking })}
            label="启用推理模式"
          />
        </div>
      )}
      {!global && endpoint.mode === "inherit" && (
        <div className="settings-form-grid">
          <label className="field">
            <span>该能力温度</span>
            <input
              type="number"
              min="0"
              max="2"
              step="0.1"
              value={endpoint.temperature}
              onChange={(event) =>
                onEndpoint({ ...endpoint, temperature: Number(event.target.value) })
              }
            />
          </label>
          <label className="field">
            <span>该能力超时（秒）</span>
            <input
              type="number"
              min="5"
              max="600"
              value={endpoint.timeout_seconds}
              onChange={(event) =>
                onEndpoint({ ...endpoint, timeout_seconds: Number(event.target.value) })
              }
            />
          </label>
          <Toggle
            checked={endpoint.thinking}
            onChange={(thinking) => onEndpoint({ ...endpoint, thinking })}
            label="该能力启用推理模式"
            description="接入地址、模型和密钥继承全局；执行参数单独生效。字幕批处理通常建议关闭。"
          />
        </div>
      )}
      <footer className="config-slab-actions">
        <button
          className="button button-secondary"
          type="button"
          disabled={testing || (!global && endpoint.mode === "disabled")}
          onClick={onTest}
        >
          {testing ? "正在测试…" : "测试连接"}
        </button>
        {testNotice && <span role="status">{testNotice}</span>}
      </footer>
    </article>
  );
}

function PromptEditor({
  title,
  description,
  value,
  onChange
}: {
  title: string;
  description: string;
  value: PromptEntry;
  onChange: (next: PromptEntry) => void;
}) {
  return (
    <article className="prompt-editor">
      <header>
        <h3>{title}</h3>
        <p>{description}</p>
      </header>
      <label className="field">
        <span>组合方式</span>
        <select
          value={value.mode}
          onChange={(event) =>
            onChange({ ...value, mode: event.target.value as PromptEntry["mode"] })
          }
        >
          <option value="builtin">使用内置指令</option>
          <option value="append">追加到内置指令</option>
          <option value="replace">完全替换内置指令</option>
        </select>
      </label>
      <label className="field">
        <span>自定义指令</span>
        <textarea
          rows={7}
          value={value.text}
          disabled={value.mode === "builtin"}
          onChange={(event) => onChange({ ...value, text: event.target.value })}
          placeholder={
            value.mode === "builtin"
              ? "当前使用内置版本"
              : "描述输出格式、术语、语气和禁止事项…"
          }
        />
      </label>
    </article>
  );
}

export default function SettingsPage() {
  const queryClient = useQueryClient();
  const [searchParams, setSearchParams] = useSearchParams();
  const requestedTab = searchParams.get("section");
  const initialTab =
    requestedTab && requestedTab in tabLabels
      ? (requestedTab as SettingsTab)
      : "review";
  const [tab, setTab] = useState<SettingsTab>(initialTab);
  const [draft, setDraft] = useState<ApplicationSettings>();
  const [secrets, setSecrets] = useState<Record<string, string>>({});
  const [notice, setNotice] = useState("");
  const [testNotice, setTestNotice] = useState<Record<string, string>>({});

  const settings = useQuery({
    queryKey: ["settings"],
    queryFn: api.settings
  });

  useEffect(() => {
    if (settings.data) {
      setDraft(structuredClone(settings.data));
    }
  }, [settings.data]);

  useEffect(() => {
    const next = searchParams.get("section");
    if (next && next in tabLabels && next !== tab) {
      setTab(next as SettingsTab);
    }
  }, [searchParams, tab]);

  const save = useMutation({
    mutationFn: api.updateSettings,
    onSuccess: async (value) => {
      setDraft(structuredClone(value));
      setSecrets({});
      setNotice(`配置版本 v${value.version} 已保存；新任务将使用此版本快照。`);
      await queryClient.invalidateQueries({ queryKey: ["settings"] });
    },
    onError: (error) => setNotice(message(error, "配置保存失败"))
  });

  const testConnection = useMutation({
    mutationFn: api.testConnection,
    onSuccess: (result) =>
      setTestNotice((current) => ({
        ...current,
        [result.target]: `${result.message} · ${result.latency_ms}ms`
      })),
    onError: (error, target) =>
      setTestNotice((current) => ({
        ...current,
        [target]: message(error, "连接测试失败")
      }))
  });

  if (settings.isPending || !draft) {
    return <LoadingBlock label="正在读取版本化配置" />;
  }
  if (settings.isError) {
    return (
      <QueryError
        title="设置中心暂时不可用"
        message={message(settings.error, "无法读取系统设置")}
        retry={() => void settings.refetch()}
      />
    );
  }

  const updateModel = (key: ModelKey, value: ModelEndpoint) => {
    setDraft((current) =>
      current
        ? { ...current, models: { ...current.models, [key]: value } }
        : current
    );
  };
  const updatePrompt = (key: PromptKey, value: PromptEntry) => {
    setDraft((current) =>
      current
        ? { ...current, prompts: { ...current.prompts, [key]: value } }
        : current
    );
  };

  const submit = () => {
    setNotice("");
    const {
      version,
      secret_configured: _secretConfigured,
      updated_at: _updatedAt,
      ...config
    } = draft;
    const input: UpdateSettingsInput = {
      expected_version: version,
      ...config,
      secrets: Object.fromEntries(
        Object.entries(secrets).filter(([, value]) => value.trim())
      )
    };
    save.mutate(input);
  };

  const modelMeta: Record<ModelKey, [string, string]> = {
    global: ["全局模型", "元数据与所有继承型能力的默认入口。"],
    subtitle_translation: ["字幕翻译专用模型", "覆盖全局模型，专门处理批量字幕翻译。"],
    subtitle_qc: ["字幕质检专用模型", "独立评估漏译、错译、时间轴与可读性。"],
    smart_segmentation: ["智能分段专用模型", "按语义、时长和 CPS 重排字幕片段。"]
  };
  const asrLanguageMismatch =
    draft.subtitle.asr.enabled &&
    !languageHintMatches(
      draft.subtitle.source_language,
      draft.subtitle.asr.language
    );

  return (
    <>
      <PageHeader
        title="处理策略与服务接入"
        description={`当前版本 v${draft.version}。保存后只影响新任务；运行中的任务继续使用创建时快照。`}
        actions={
          <button
            className="button button-primary"
            type="button"
            disabled={save.isPending}
            onClick={submit}
          >
            {save.isPending ? "正在保存…" : "保存新版本"}
          </button>
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

      <div className="settings-workbench">
        <nav className="settings-index" aria-label="设置分组">
          {(Object.keys(tabLabels) as SettingsTab[]).map((key) => (
            <button
              type="button"
              className={tab === key ? "is-active" : ""}
              aria-current={tab === key ? "page" : undefined}
              onClick={() => {
                setTab(key);
                setSearchParams({ section: key }, { replace: true });
              }}
              key={key}
            >
              <span><Icon name={tabLabels[key].icon} /></span>
              <strong>{tabLabels[key].label}</strong>
              <small>{tabLabels[key].note}</small>
            </button>
          ))}
          <p>
            密钥仅写入加密存储，不会回显。页面中的“已配置”只表示存在密文。
          </p>
        </nav>

        <div className="settings-stage">
          {tab === "review" && (
            <>
              <SettingsSection
                icon="review"
                title="审核路径"
                description="手动模式进入审核队列；自动模式逐条执行规则并保存判定结果。"
              >
                <div className="mode-selector">
                  <label>
                    <input
                      type="radio"
                      checked={draft.review.mode === "manual"}
                      onChange={() =>
                        setDraft({
                          ...draft,
                          review: { ...draft.review, mode: "manual" }
                        })
                      }
                    />
                    <span>
                      <strong>人工审核</strong>
                      <small>媒体、元数据和字幕完成后等待运营人员批准。</small>
                    </span>
                  </label>
                  <label>
                    <input
                      type="radio"
                      checked={draft.review.mode === "automatic"}
                      onChange={() =>
                        setDraft({
                          ...draft,
                          review: { ...draft.review, mode: "automatic" }
                        })
                      }
                    />
                    <span>
                      <strong>自动审核</strong>
                      <small>规则通过后直接进入待发布；失败按回退策略处理。</small>
                    </span>
                  </label>
                </div>
                {draft.review.mode === "automatic" && (
                  <label className="field">
                    <span>规则失败后</span>
                    <select
                      value={draft.review.automatic_fallback}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          review: {
                            ...draft.review,
                            automatic_fallback: event.target.value as "manual" | "reject"
                          }
                        })
                      }
                    >
                      <option value="manual">转入人工审核</option>
                      <option value="reject">拒绝并将任务标记失败</option>
                    </select>
                  </label>
                )}
              </SettingsSection>

              <SettingsSection
                icon="sliders"
                title="自动审核规则"
                description="每次运行都会保存期望值、实际值和通过状态，便于复核。"
              >
                <div className="rule-grid">
                  <Toggle
                    checked={draft.review.rules.require_media}
                    onChange={(require_media) =>
                      setDraft({
                        ...draft,
                        review: {
                          ...draft.review,
                          rules: { ...draft.review.rules, require_media }
                        }
                      })
                    }
                    label="必须有可用媒体"
                  />
                  <Toggle
                    checked={draft.review.rules.require_title}
                    onChange={(require_title) =>
                      setDraft({
                        ...draft,
                        review: {
                          ...draft.review,
                          rules: { ...draft.review.rules, require_title }
                        }
                      })
                    }
                    label="标题不能为空"
                  />
                  <Toggle
                    checked={draft.review.rules.require_subtitle_qc}
                    onChange={(require_subtitle_qc) =>
                      setDraft({
                        ...draft,
                        review: {
                          ...draft.review,
                          rules: { ...draft.review.rules, require_subtitle_qc }
                        }
                      })
                    }
                    label="字幕必须通过质检"
                  />
                  <label className="field">
                    <span>简介最少字符</span>
                    <input
                      type="number"
                      min="0"
                      value={draft.review.rules.minimum_description_length}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          review: {
                            ...draft.review,
                            rules: {
                              ...draft.review.rules,
                              minimum_description_length: Number(event.target.value)
                            }
                          }
                        })
                      }
                    />
                  </label>
                  <label className="field">
                    <span>最大时长（秒，0 不限制）</span>
                    <input
                      type="number"
                      min="0"
                      value={draft.review.rules.maximum_duration_seconds}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          review: {
                            ...draft.review,
                            rules: {
                              ...draft.review.rules,
                              maximum_duration_seconds: Number(event.target.value)
                            }
                          }
                        })
                      }
                    />
                  </label>
                  <label className="field">
                    <span>字幕 QC 最低分</span>
                    <input
                      type="number"
                      min="0"
                      max="100"
                      value={draft.review.rules.minimum_subtitle_qc_score}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          review: {
                            ...draft.review,
                            rules: {
                              ...draft.review.rules,
                              minimum_subtitle_qc_score: Number(event.target.value)
                            }
                          }
                        })
                      }
                    />
                  </label>
                </div>
              </SettingsSection>
            </>
          )}

          {tab === "automation" && (
            <>
              <SettingsSection
                icon="route"
                title="常规处理自动化"
                description="控制媒体信息完成后是否自动执行元数据加工、字幕、封面与后续处理。每个任务保存独立快照。"
              >
                <Toggle
                  checked={draft.automation.enabled}
                  onChange={(enabled) =>
                    setDraft({
                      ...draft,
                      automation: { ...draft.automation, enabled }
                    })
                  }
                  label="启用处理自动化"
                  description="关闭时仍可创建任务，但需要按任务步骤人工继续。"
                />
                <div className="rule-grid">
                  <Toggle
                    checked={draft.automation.translate_title}
                    onChange={(translate_title) =>
                      setDraft({
                        ...draft,
                        automation: { ...draft.automation, translate_title }
                      })
                    }
                    label="翻译标题"
                  />
                  <Toggle
                    checked={draft.automation.translate_description}
                    onChange={(translate_description) =>
                      setDraft({
                        ...draft,
                        automation: {
                          ...draft.automation,
                          translate_description
                        }
                      })
                    }
                    label="翻译简介"
                  />
                  <Toggle
                    checked={draft.automation.generate_tags}
                    onChange={(generate_tags) =>
                      setDraft({
                        ...draft,
                        automation: { ...draft.automation, generate_tags }
                      })
                    }
                    label="生成标签"
                  />
                  <Toggle
                    checked={draft.automation.recommend_categories}
                    onChange={(recommend_categories) =>
                      setDraft({
                        ...draft,
                        automation: {
                          ...draft.automation,
                          recommend_categories
                        }
                      })
                    }
                    label="推荐平台分区"
                  />
                  <Toggle
                    checked={draft.automation.process_cover}
                    onChange={(process_cover) =>
                      setDraft({
                        ...draft,
                        automation: { ...draft.automation, process_cover }
                      })
                    }
                    label="处理封面"
                  />
                </div>
              </SettingsSection>

              <SettingsSection
                icon="review"
                title="完整流水线说明"
                description="全自动投稿不是单个开关，而是任务策略、内容处理、审核和平台发布共同组成的状态机。"
              >
                <ol className="automation-flow">
                  <li><strong>发现或建单</strong><span>YouTube 全站检索、频道监控或手动 URL。</span></li>
                  <li><strong>下载与检测</strong><span>读取登录配置，下载媒体并检测格式与时长。</span></li>
                  <li><strong>字幕与媒体处理</strong><span>语音识别、分段、翻译、质检、格式转换与字幕烧录。</span></li>
                  <li><strong>内容与业务审核</strong><span>先执行内容安全检查，再按已配置规则自动审核或进入人工审核。</span></li>
                  <li><strong>平台投稿</strong><span>使用任务冻结的账号、分区和策略发布到 AcFun / Bilibili，并回查与重试。</span></li>
                </ol>
                <p className="settings-boundary-note">
                  是否审核后自动投稿由“投稿策略”和每条任务的“全自动”选项决定；这里的开关不会绕过审核。
                </p>
              </SettingsSection>
            </>
          )}

          {tab === "models" && (
              <SettingsSection
                icon="model"
              title="全局模型与专用覆盖"
              description="四层均使用 OpenAI 兼容协议；专用层可继承、覆盖或禁用。"
            >
              <div className="model-stack">
                {(Object.keys(modelMeta) as ModelKey[]).map((key) => (
                  <ModelEditor
                    key={key}
                    modelKey={key}
                    title={modelMeta[key][0]}
                    description={modelMeta[key][1]}
                    endpoint={draft.models[key]}
                    configured={Boolean(draft.secret_configured[secretKeys[key]])}
                    secret={secrets[secretKeys[key]] ?? ""}
                    onEndpoint={(value) => updateModel(key, value)}
                    onSecret={(value) =>
                      setSecrets((current) => ({ ...current, [secretKeys[key]]: value }))
                    }
                    onTest={() => testConnection.mutate(key)}
                    testing={testConnection.isPending && testConnection.variables === key}
                    testNotice={testNotice[key]}
                  />
                ))}
              </div>
            </SettingsSection>
          )}

          {tab === "subtitles" && (
            <>
              <SettingsSection
                icon="subtitles"
                title="字幕来源与语音识别"
                description="先使用视频已有字幕；没有合适字幕时，再从声音生成文字。"
                help={
                  <HelpLink label="配置与申请" title="如何配置语音识别服务">
                    <p>语音识别用于视频没有可用字幕时，从声音生成带时间轴的字幕。</p>
                    <ol>
                      <li>在阿里云百炼开通模型服务并创建 API Key。</li>
                      <li>选择“阿里云百炼 Paraformer”，粘贴服务地址、模型名和 API Key。</li>
                      <li>语言不确定时选择“自动识别”，保存后点击“测试连接”。</li>
                    </ol>
                    <div className="guide-links">
                      <ExternalGuideLink href="https://help.aliyun.com/zh/model-studio/get-api-key">
                        阿里云 API Key 申请说明
                      </ExternalGuideLink>
                      <ExternalGuideLink href="https://help.aliyun.com/zh/model-studio/speech-recognition-api-reference/">
                        语音识别配置说明
                      </ExternalGuideLink>
                    </div>
                  </HelpLink>
                }
              >
                <Toggle
                  checked={draft.subtitle.enabled}
                  onChange={(enabled) =>
                    setDraft({ ...draft, subtitle: { ...draft.subtitle, enabled } })
                  }
                  label="启用字幕流水线"
                  description="下载完成后创建独立字幕步骤；失败可单独重试。"
                />
                <div className="config-slab existing-subtitle-settings">
                  <header>
                    <div>
                      <p className="eyebrow">处理前检查</p>
                      <h3>已有中文字幕识别</h3>
                      <p>
                        先检查平台字幕、媒体字幕轨与画面硬字幕；确认已有中文字幕后不再调用翻译模型。
                      </p>
                    </div>
                    <span
                      className={`config-readiness ${
                        draft.subtitle.existing_chinese.enabled ? "is-on" : ""
                      }`}
                    >
                      {draft.subtitle.existing_chinese.enabled ? "已启用" : "未启用"}
                    </span>
                  </header>
                  <Toggle
                    checked={draft.subtitle.existing_chinese.enabled}
                    onChange={(enabled) =>
                      setDraft({
                        ...draft,
                        subtitle: {
                          ...draft.subtitle,
                          existing_chinese: {
                            ...draft.subtitle.existing_chinese,
                            enabled
                          }
                        }
                      })
                    }
                    label="优先识别已有中文字幕"
                    description="检测异常或证据不足时自动继续原 ASR 与翻译流程，不会误跳过。"
                  />
                  {draft.subtitle.existing_chinese.enabled && (
                    <>
                      <div className="settings-form-grid existing-subtitle-sources">
                        {existingSubtitleSources.map(([key, label, description]) => (
                          <Toggle
                            key={key}
                            checked={Boolean(
                              draft.subtitle.existing_chinese[key]
                            )}
                            onChange={(checked) =>
                              setDraft({
                                ...draft,
                                subtitle: {
                                  ...draft.subtitle,
                                  existing_chinese: {
                                    ...draft.subtitle.existing_chinese,
                                    [key]: checked
                                  }
                                }
                              })
                            }
                            label={label}
                            description={description}
                          />
                        ))}
                      </div>
                      <div className="settings-form-grid">
                        <label className="field">
                          <span>抽帧数量</span>
                          <input
                            type="number"
                            min={8}
                            max={120}
                            value={draft.subtitle.existing_chinese.sample_count}
                            onChange={(event) =>
                              setDraft({
                                ...draft,
                                subtitle: {
                                  ...draft.subtitle,
                                  existing_chinese: {
                                    ...draft.subtitle.existing_chinese,
                                    sample_count: Number(event.target.value)
                                  }
                                }
                              })
                            }
                          />
                          <small>建议 32 帧；长视频会均匀覆盖开头、中段和结尾。</small>
                        </label>
                        <label className="field">
                          <span>文字置信度（%）</span>
                          <input
                            type="number"
                            min={50}
                            max={99}
                            value={
                              draft.subtitle.existing_chinese
                                .confidence_threshold_percent
                            }
                            onChange={(event) =>
                              setDraft({
                                ...draft,
                                subtitle: {
                                  ...draft.subtitle,
                                  existing_chinese: {
                                    ...draft.subtitle.existing_chinese,
                                    confidence_threshold_percent: Number(event.target.value)
                                  }
                                }
                              })
                            }
                          />
                          <small>低于阈值的文字不作为自动跳过依据。</small>
                        </label>
                        <label className="field">
                          <span>稳定覆盖率（%）</span>
                          <input
                            type="number"
                            min={20}
                            max={100}
                            value={
                              draft.subtitle.existing_chinese.coverage_threshold_percent
                            }
                            onChange={(event) =>
                              setDraft({
                                ...draft,
                                subtitle: {
                                  ...draft.subtitle,
                                  existing_chinese: {
                                    ...draft.subtitle.existing_chinese,
                                    coverage_threshold_percent: Number(event.target.value)
                                  }
                                }
                              })
                            }
                          />
                          <small>相邻画面必须持续出现字幕，默认要求覆盖 60% 的抽检组。</small>
                        </label>
                        <label className="field">
                          <span>不同字幕文本数</span>
                          <input
                            type="number"
                            min={2}
                            max={30}
                            value={draft.subtitle.existing_chinese.minimum_distinct_texts}
                            onChange={(event) =>
                              setDraft({
                                ...draft,
                                subtitle: {
                                  ...draft.subtitle,
                                  existing_chinese: {
                                    ...draft.subtitle.existing_chinese,
                                    minimum_distinct_texts: Number(event.target.value)
                                  }
                                }
                              })
                            }
                          />
                          <small>至少出现多条不同文本，避免把固定水印当作字幕。</small>
                        </label>
                      </div>
                      <p className="settings-policy-note">
                        命中可用中文字幕：直接复用并跳过翻译和重复烧录；未命中时才进入语音识别与后续处理。
                        证据不足或检测失败：继续原流水线。
                      </p>
                    </>
                  )}
                </div>
                <div className="settings-form-grid">
                  <label className="field">
                    <span>来源策略</span>
                    <select
                      value={draft.subtitle.source_strategy}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          subtitle: {
                            ...draft.subtitle,
                            source_strategy: event.target
                              .value as ApplicationSettings["subtitle"]["source_strategy"]
                          }
                        })
                      }
                    >
                      <option value="youtube_then_asr">YouTube 字幕优先，ASR 回退</option>
                      <option value="youtube_manual_then_asr">人工字幕优先，ASR 回退</option>
                      <option value="youtube_only">只使用 YouTube 字幕</option>
                      <option value="asr_only">只使用 ASR</option>
                    </select>
                  </label>
                  <label className="field">
                    <span>源语言</span>
                    <LanguageSelect
                      value={draft.subtitle.source_language}
                      onChange={(source_language) =>
                        setDraft({
                          ...draft,
                          subtitle: {
                            ...draft.subtitle,
                            source_language
                          }
                        })
                      }
                    />
                  </label>
                  <label className="field">
                    <span>目标语言</span>
                    <LanguageSelect
                      value={draft.subtitle.target_language}
                      allowAuto={false}
                      onChange={(target_language) =>
                        setDraft({
                          ...draft,
                          subtitle: {
                            ...draft.subtitle,
                            target_language
                          }
                        })
                      }
                    />
                  </label>
                  <Toggle
                    checked={draft.subtitle.download_auto_subtitles}
                    onChange={(download_auto_subtitles) =>
                      setDraft({
                        ...draft,
                        subtitle: { ...draft.subtitle, download_auto_subtitles }
                      })
                    }
                    label="允许 YouTube 自动字幕"
                  />
                  <Toggle
                    checked={draft.subtitle.asr.enabled}
                    onChange={(enabled) =>
                      setDraft({
                        ...draft,
                        subtitle: {
                          ...draft.subtitle,
                          asr: { ...draft.subtitle.asr, enabled }
                        }
                      })
                    }
                    label="启用 ASR 回退"
                  />
                </div>
                {draft.subtitle.asr.enabled && (
                  <div className="config-slab">
                    <div className="settings-form-grid">
                      <label className="field">
                        <span>ASR 提供商</span>
                        <select
                          value={draft.subtitle.asr.provider}
                          onChange={(event) =>
                            setDraft({
                              ...draft,
                              subtitle: {
                                ...draft.subtitle,
                                asr: {
                                  ...draft.subtitle.asr,
                                  provider: event.target
                                    .value as ApplicationSettings["subtitle"]["asr"]["provider"]
                                }
                              }
                            })
                          }
                        >
                          <option value="openai_compatible">Whisper / OpenAI 兼容</option>
                          <option value="voxtral">Voxtral / Mistral 兼容</option>
                          <option value="aliyun_paraformer">阿里云百炼 Paraformer</option>
                          <option value="fixture">演示模式（不连接外部服务）</option>
                        </select>
                      </label>
                      <label className="field field-wide">
                        <span>
                          {draft.subtitle.asr.provider === "aliyun_paraformer"
                            ? "DashScope API 地址"
                            : "ASR Base URL"}
                        </span>
                        <input
                          type="url"
                          value={draft.subtitle.asr.base_url}
                          onChange={(event) =>
                            setDraft({
                              ...draft,
                              subtitle: {
                                ...draft.subtitle,
                                asr: {
                                  ...draft.subtitle.asr,
                                  base_url: event.target.value
                                }
                              }
                            })
                          }
                          placeholder={
                            draft.subtitle.asr.provider === "aliyun_paraformer"
                              ? "https://…maas.aliyuncs.com/api/v1"
                              : "https://api.openai.com/v1"
                          }
                        />
                      </label>
                      <label className="field">
                        <span>ASR 模型</span>
                        <input
                          value={draft.subtitle.asr.model}
                          onChange={(event) =>
                            setDraft({
                              ...draft,
                              subtitle: {
                                ...draft.subtitle,
                                asr: { ...draft.subtitle.asr, model: event.target.value }
                              }
                            })
                          }
                        />
                      </label>
                      <label className="field field-wide">
                        <span>ASR API Key</span>
                        <input
                          type="password"
                          value={secrets["subtitle.asr.api_key"] ?? ""}
                          onChange={(event) =>
                            setSecrets((current) => ({
                              ...current,
                              "subtitle.asr.api_key": event.target.value
                            }))
                          }
                          placeholder={
                            draft.secret_configured["subtitle.asr.api_key"]
                              ? "已配置；留空保持不变"
                              : "尚未配置"
                          }
                        />
                      </label>
                      <label className="field">
                        <span>语言提示</span>
                        <LanguageSelect
                          value={draft.subtitle.asr.language}
                          onChange={(language) =>
                            setDraft({
                              ...draft,
                              subtitle: {
                                ...draft.subtitle,
                                asr: {
                                  ...draft.subtitle.asr,
                                  language
                                }
                              }
                            })
                          }
                        />
                      </label>
                      {asrLanguageMismatch && (
                        <p className="field-error field-wide" role="alert">
                          当前源语言为 {draft.subtitle.source_language}，ASR 语言提示却是{" "}
                          {draft.subtitle.asr.language}。请包含源语言，或改为 auto，否则会明显降低识别质量。
                        </p>
                      )}
                      <label className="field">
                        <span>任务超时（秒）</span>
                        <input
                          type="number"
                          min="10"
                          max="3600"
                          value={draft.subtitle.asr.timeout_seconds}
                          onChange={(event) =>
                            setDraft({
                              ...draft,
                              subtitle: {
                                ...draft.subtitle,
                                asr: {
                                  ...draft.subtitle.asr,
                                  timeout_seconds: Number(event.target.value)
                                }
                              }
                            })
                          }
                        />
                      </label>
                      <label className="field">
                        <span>请求重试次数</span>
                        <input
                          type="number"
                          min="0"
                          max="10"
                          value={draft.subtitle.asr.max_retries}
                          onChange={(event) =>
                            setDraft({
                              ...draft,
                              subtitle: {
                                ...draft.subtitle,
                                asr: {
                                  ...draft.subtitle.asr,
                                  max_retries: Number(event.target.value)
                                }
                              }
                            })
                          }
                        />
                      </label>
                      {draft.subtitle.asr.provider === "aliyun_paraformer" ? (
                        <p className="settings-boundary-note field-wide">
                          本地运行会先把提取出的音频上传到百炼签发的临时 OSS 地址，再异步轮询
                          Paraformer。连接测试只校验密钥和上传通道，不会提交计费转写。
                        </p>
                      ) : (
                        <>
                          <label className="field">
                            <span>分片窗口（秒）</span>
                            <input
                              type="number"
                              min="30"
                              max="3600"
                              value={draft.subtitle.asr.chunk_seconds}
                              onChange={(event) =>
                                setDraft({
                                  ...draft,
                                  subtitle: {
                                    ...draft.subtitle,
                                    asr: {
                                      ...draft.subtitle.asr,
                                      chunk_seconds: Number(event.target.value)
                                    }
                                  }
                                })
                              }
                            />
                          </label>
                          <label className="field">
                            <span>分片重叠（秒）</span>
                            <input
                              type="number"
                              min="0"
                              value={draft.subtitle.asr.chunk_overlap_seconds}
                              onChange={(event) =>
                                setDraft({
                                  ...draft,
                                  subtitle: {
                                    ...draft.subtitle,
                                    asr: {
                                      ...draft.subtitle.asr,
                                      chunk_overlap_seconds: Number(event.target.value)
                                    }
                                  }
                                })
                              }
                            />
                          </label>
                          <Toggle
                            checked={draft.subtitle.asr.vad_enabled}
                            onChange={(vad_enabled) =>
                              setDraft({
                                ...draft,
                                subtitle: {
                                  ...draft.subtitle,
                                  asr: { ...draft.subtitle.asr, vad_enabled }
                                }
                              })
                            }
                            label="启用 VAD 扫描窗"
                          />
                        </>
                      )}
                    </div>
                    <div className="config-slab-actions">
                      <button
                        className="button button-secondary"
                        type="button"
                        onClick={() => testConnection.mutate("asr")}
                        disabled={testConnection.isPending}
                      >
                        测试 ASR 连接
                      </button>
                      {testNotice.asr && <span>{testNotice.asr}</span>}
                    </div>
                  </div>
                )}
              </SettingsSection>

              <SettingsSection
                icon="timeRange"
                title="时间轴后处理"
                description="统一字幕出现时间、停留时长、断句和每行字数，让字幕更易读。"
                help={
                  <HelpLink title="时间轴后处理有什么用">
                    <p>这一步不重新翻译内容，只整理字幕的时间与排版。</p>
                    <ul>
                      <li>时间偏移：整体修正声音与字幕提前或延后的问题。</li>
                      <li>最短显示：避免字幕一闪而过。</li>
                      <li>合并间隔：把间隔很短的连续短句合并。</li>
                      <li>每行字数与行数：控制手机和网页上的阅读密度。</li>
                    </ul>
                  </HelpLink>
                }
              >
                <div className="rule-grid">
                  {(
                    [
                      ["time_offset_seconds", "时间偏移（秒）", -60, 60, 0.1],
                      ["minimum_cue_seconds", "最短显示（秒）", 0.1, 30, 0.1],
                      ["merge_gap_seconds", "合并间隙（秒）", 0, 10, 0.05],
                      ["minimum_text_length", "最小文本长度", 1, 100, 1],
                      ["maximum_characters_per_line", "每行最大字符", 5, 100, 1],
                      ["maximum_lines", "最大行数", 1, 4, 1]
                    ] as const
                  ).map(([key, label, min, max, step]) => (
                    <label className="field" key={key}>
                      <span>{label}</span>
                      <input
                        type="number"
                        min={min}
                        max={max}
                        step={step}
                        value={draft.subtitle.postprocess[key]}
                        onChange={(event) =>
                          setDraft({
                            ...draft,
                            subtitle: {
                              ...draft.subtitle,
                              postprocess: {
                                ...draft.subtitle.postprocess,
                                [key]: Number(event.target.value)
                              }
                            }
                          })
                        }
                      />
                    </label>
                  ))}
                  <Toggle
                    checked={draft.subtitle.postprocess.normalize_punctuation}
                    onChange={(normalize_punctuation) =>
                      setDraft({
                        ...draft,
                        subtitle: {
                          ...draft.subtitle,
                          postprocess: {
                            ...draft.subtitle.postprocess,
                            normalize_punctuation
                          }
                        }
                      })
                    }
                    label="标准化标点与空白"
                  />
                  <Toggle
                    checked={draft.subtitle.postprocess.filter_filler_words}
                    onChange={(filter_filler_words) =>
                      setDraft({
                        ...draft,
                        subtitle: {
                          ...draft.subtitle,
                          postprocess: {
                            ...draft.subtitle.postprocess,
                            filter_filler_words
                          }
                        }
                      })
                    }
                    label="过滤独立填充词"
                  />
                </div>
              </SettingsSection>

              <SettingsSection
                icon="sliders"
                title="智能分段、翻译与质检"
                description="按阅读速度重新断句，翻译目标语言，并检查漏译、错译和时间轴问题。"
                help={
                  <HelpLink title="分段、翻译和质检如何配合">
                    <ol>
                      <li>智能分段先把过长字幕拆成更容易阅读的短句。</li>
                      <li>字幕翻译按批次生成目标语言字幕，并保留原时间轴。</li>
                      <li>翻译质检按完整性、可读性和时间轴一致性评分；低于阈值时进入补救或人工处理。</li>
                    </ol>
                    <p>如果已识别到合格的中文字幕，流程会直接复用并跳过不必要的翻译。</p>
                  </HelpLink>
                }
              >
                <div className="processing-switchboard">
                  <div>
                    <Toggle
                      checked={draft.subtitle.segmentation.enabled}
                      onChange={(enabled) =>
                        setDraft({
                          ...draft,
                          subtitle: {
                            ...draft.subtitle,
                            segmentation: { ...draft.subtitle.segmentation, enabled }
                          }
                        })
                      }
                      label="智能分段"
                    />
                    <label className="field">
                      <span>最长片段（秒）</span>
                      <input
                        type="number"
                        value={draft.subtitle.segmentation.maximum_cue_seconds}
                        onChange={(event) =>
                          setDraft({
                            ...draft,
                            subtitle: {
                              ...draft.subtitle,
                              segmentation: {
                                ...draft.subtitle.segmentation,
                                maximum_cue_seconds: Number(event.target.value)
                              }
                            }
                          })
                        }
                      />
                    </label>
                    <label className="field">
                      <span>最大 CPS</span>
                      <input
                        type="number"
                        value={draft.subtitle.segmentation.maximum_cps}
                        onChange={(event) =>
                          setDraft({
                            ...draft,
                            subtitle: {
                              ...draft.subtitle,
                              segmentation: {
                                ...draft.subtitle.segmentation,
                                maximum_cps: Number(event.target.value)
                              }
                            }
                          })
                        }
                      />
                    </label>
                  </div>
                  <div>
                    <Toggle
                      checked={draft.subtitle.translation.enabled}
                      onChange={(enabled) =>
                        setDraft({
                          ...draft,
                          subtitle: {
                            ...draft.subtitle,
                            translation: { ...draft.subtitle.translation, enabled }
                          }
                        })
                      }
                      label="字幕翻译"
                    />
                    <label className="field">
                      <span>翻译批次</span>
                      <input
                        type="number"
                        value={draft.subtitle.translation.batch_size}
                        onChange={(event) =>
                          setDraft({
                            ...draft,
                            subtitle: {
                              ...draft.subtitle,
                              translation: {
                                ...draft.subtitle.translation,
                                batch_size: Number(event.target.value)
                              }
                            }
                          })
                        }
                      />
                    </label>
                  </div>
                  <div>
                    <Toggle
                      checked={draft.subtitle.qc.enabled}
                      onChange={(enabled) =>
                        setDraft({
                          ...draft,
                          subtitle: {
                            ...draft.subtitle,
                            qc: { ...draft.subtitle.qc, enabled }
                          }
                        })
                      }
                      label="字幕翻译质检"
                    />
                    <label className="field">
                      <span>通过阈值</span>
                      <input
                        type="number"
                        min="0"
                        max="100"
                        value={draft.subtitle.qc.threshold}
                        onChange={(event) =>
                          setDraft({
                            ...draft,
                            subtitle: {
                              ...draft.subtitle,
                              qc: {
                                ...draft.subtitle.qc,
                                threshold: Number(event.target.value)
                              }
                            }
                          })
                        }
                      />
                    </label>
                  </div>
                </div>
                <div className="rule-grid">
                  <Toggle
                    checked={draft.subtitle.keep_original}
                    onChange={(keep_original) =>
                      setDraft({
                        ...draft,
                        subtitle: { ...draft.subtitle, keep_original }
                      })
                    }
                    label="保留原始字幕"
                  />
                  <Toggle
                    checked={draft.subtitle.embed_in_video}
                    onChange={(embed_in_video) =>
                      setDraft({
                        ...draft,
                        subtitle: { ...draft.subtitle, embed_in_video }
                      })
                    }
                    label="后续烧录到视频"
                    description="当前会保存配置；媒体派生阶段接入后执行烧录。"
                  />
                </div>
              </SettingsSection>
            </>
          )}

          {tab === "prompts" && (
              <SettingsSection
                icon="prompt"
              title="内容处理规则"
              description="为翻译、分段、质检和简介生成设定长期复用的文字规则。"
              help={
                <HelpLink title="提示词模块有什么用">
                  <p>这里用于规定术语、语气、输出格式和质检重点，不需要修改程序。</p>
                  <p>每次保存都会生成新版本；已经开始的任务继续使用创建时的规则，避免处理中途结果发生变化。</p>
                </HelpLink>
              }
            >
              <div className="prompt-grid">
                {(
                  [
                    ["subtitle_translation", "字幕翻译主提示词", "术语、语气和索引完整性。"],
                    [
                      "subtitle_translation_strict",
                      "严格补救提示词",
                      "翻译缺项时的第二次强制修复。"
                    ],
                    ["subtitle_qc", "字幕 QC 提示词", "错译、漏译、时间轴和可读性检查。"],
                    ["smart_segmentation", "智能分段提示词", "语义断句、时长和 CPS 约束。"],
                    ["metadata_translation", "元数据翻译提示词", "标题、简介和标签翻译。"],
                    [
                      "metadata_description_retry",
                      "简介重试提示词",
                      "简介生成不合规时的补救指令。"
                    ]
                  ] as [PromptKey, string, string][]
                ).map(([key, title, description]) => (
                  <PromptEditor
                    key={key}
                    title={title}
                    description={description}
                    value={draft.prompts[key]}
                    onChange={(value) => updatePrompt(key, value)}
                  />
                ))}
              </div>
            </SettingsSection>
          )}

          {tab === "transcode" && (
            <>
              <SettingsSection
                icon="media"
                title="全局视频处理"
                description="统一视频格式、画质、音频质量和字幕呈现；平台专用设置可覆盖这里的默认值。"
              >
                <Toggle
                  checked={draft.transcode.enabled}
                  onChange={(enabled) =>
                    setDraft({
                      ...draft,
                      transcode: { ...draft.transcode, enabled }
                    })
                  }
                  label="启用视频转码"
                  description="投稿策略选中专用预设时，以任务冻结的专用预设覆盖这里的默认值。"
                />
                <div className="settings-form-grid">
                  <label className="field">
                    <span>编码模式</span>
                    <select
                      value={draft.transcode.encoder_mode}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          transcode: {
                            ...draft.transcode,
                            encoder_mode: event.target.value
                          }
                        })
                      }
                    >
                      <option value="auto">推荐模式（自动选择）</option>
                      <option value="cpu">通用模式</option>
                      <option value="nvidia">NVIDIA 显卡加速</option>
                      <option value="intel">Intel 显卡加速</option>
                      <option value="amd">AMD 显卡加速</option>
                    </select>
                  </label>
                  <label className="field">
                    <span>视频编码</span>
                    <select
                      value={draft.transcode.video_codec}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          transcode: {
                            ...draft.transcode,
                            video_codec: event.target.value
                          }
                        })
                      }
                    >
                      <option value="h264">H.264（兼容性最好）</option>
                      <option value="copy">不重编码</option>
                    </select>
                  </label>
                  <label className="field">
                    <span>音频编码</span>
                    <select
                      value={draft.transcode.audio_codec}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          transcode: {
                            ...draft.transcode,
                            audio_codec: event.target.value
                          }
                        })
                      }
                    >
                      <option value="aac">AAC</option>
                      <option value="copy">不重编码</option>
                    </select>
                  </label>
                  <label className="field">
                    <span>容器</span>
                    <select
                      value={draft.transcode.container}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          transcode: {
                            ...draft.transcode,
                            container: event.target.value
                          }
                        })
                      }
                    >
                      <option value="mp4">MP4</option>
                      <option value="mkv">Matroska</option>
                    </select>
                  </label>
                  <label className="field">
                    <span>最大高度</span>
                    <input
                      type="number"
                      min="360"
                      max="4320"
                      value={draft.transcode.maximum_height}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          transcode: {
                            ...draft.transcode,
                            maximum_height: Number(event.target.value)
                          }
                        })
                      }
                    />
                  </label>
                  <label className="field">
                    <span>视频码率（Kbps）</span>
                    <input
                      type="number"
                      min="200"
                      value={draft.transcode.video_bitrate_kbps}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          transcode: {
                            ...draft.transcode,
                            video_bitrate_kbps: Number(event.target.value)
                          }
                        })
                      }
                    />
                  </label>
                  <label className="field">
                    <span>音频码率（Kbps）</span>
                    <input
                      type="number"
                      min="32"
                      value={draft.transcode.audio_bitrate_kbps}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          transcode: {
                            ...draft.transcode,
                            audio_bitrate_kbps: Number(event.target.value)
                          }
                        })
                      }
                    />
                  </label>
                  <label className="field">
                    <span>处理速度</span>
                    <select
                      value={draft.transcode.cpu_preset}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          transcode: {
                            ...draft.transcode,
                            cpu_preset: event.target.value
                          }
                        })
                      }
                    >
                      <option value="ultrafast">最快（文件较大）</option>
                      <option value="veryfast">较快</option>
                      <option value="fast">均衡偏快</option>
                      <option value="medium">均衡</option>
                      <option value="slow">精细（耗时较长）</option>
                    </select>
                  </label>
                  <label className="field">
                    <span>高分辨率处理速度</span>
                    <select
                      value={draft.transcode.high_resolution_cpu_preset}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          transcode: {
                            ...draft.transcode,
                            high_resolution_cpu_preset: event.target.value
                          }
                        })
                      }
                    >
                      <option value="ultrafast">最快（文件较大）</option>
                      <option value="veryfast">较快</option>
                      <option value="fast">均衡偏快</option>
                      <option value="medium">均衡</option>
                      <option value="slow">精细（耗时较长）</option>
                    </select>
                  </label>
                  <Toggle
                    checked={draft.transcode.burn_subtitles}
                    onChange={(burn_subtitles) =>
                      setDraft({
                        ...draft,
                        transcode: { ...draft.transcode, burn_subtitles }
                      })
                    }
                    label="烧录字幕"
                    description="把字幕固定显示在视频画面中，发布后无法单独关闭。"
                  />
                </div>
              </SettingsSection>

              <SettingsSection
                icon="sliders"
                title="自定义处理参数（专家）"
                description="仅在默认格式无法满足特殊平台要求时使用；系统会拒绝不安全或冲突的参数。"
              >
                <Toggle
                  checked={draft.transcode.custom_arguments_enabled}
                  onChange={(custom_arguments_enabled) =>
                    setDraft({
                      ...draft,
                      transcode: {
                        ...draft.transcode,
                        custom_arguments_enabled
                      }
                    })
                  }
                  label="启用高级参数"
                />
                <label className="field">
                  <span>高级参数（每行一个）</span>
                  <textarea
                    rows={8}
                    disabled={!draft.transcode.custom_arguments_enabled}
                    value={draft.transcode.custom_arguments.join("\n")}
                    onChange={(event) =>
                      setDraft({
                        ...draft,
                        transcode: {
                          ...draft.transcode,
                          custom_arguments: event.target.value
                            .split("\n")
                            .map((value) => value.trim())
                            .filter(Boolean)
                        }
                      })
                    }
                  />
                </label>
                <Link className="button button-secondary" to="/publishing/settings">
                  管理投稿专用转码预设
                </Link>
              </SettingsSection>
            </>
          )}

          {tab === "moderation" && (
            <>
              <SettingsSection
                icon="shield"
                title="内容安全服务"
                description="审核发生在媒体与字幕处理之后、业务自动/人工审核之前；结果会展示在审核详情中。"
              >
                <Toggle
                  checked={draft.moderation.enabled}
                  onChange={(enabled) =>
                    setDraft({
                      ...draft,
                      moderation: { ...draft.moderation, enabled }
                    })
                  }
                  label="启用内容安全审核"
                  description="投稿策略可以进一步要求此步骤必须开启。"
                />
                <div className="mode-selector">
                  <label>
                    <input
                      type="radio"
                      checked={draft.moderation.provider === "aliyun"}
                      onChange={() =>
                        setDraft({
                          ...draft,
                          moderation: {
                            ...draft.moderation,
                            provider: "aliyun"
                          }
                        })
                      }
                    />
                    <span>
                      <strong>阿里云内容安全</strong>
                      <small>真实服务，可能产生调用费用，需要公网可访问的短期签名媒体 URL。</small>
                    </span>
                  </label>
                  <label>
                    <input
                      type="radio"
                      checked={draft.moderation.provider === "fixture"}
                      onChange={() =>
                        setDraft({
                          ...draft,
                          moderation: {
                            ...draft.moderation,
                            provider: "fixture"
                          }
                        })
                      }
                    />
                    <span>
                      <strong>本地演示检查</strong>
                      <small>只用于本地流程演示，不调用外部内容审核服务。</small>
                    </span>
                  </label>
                </div>
                <div className="rule-grid">
                  <Toggle
                    checked={draft.moderation.check_text}
                    onChange={(check_text) =>
                      setDraft({
                        ...draft,
                        moderation: { ...draft.moderation, check_text }
                      })
                    }
                    label="审核标题、简介、标签和转载声明"
                  />
                  <Toggle
                    checked={draft.moderation.check_image}
                    onChange={(check_image) =>
                      setDraft({
                        ...draft,
                        moderation: { ...draft.moderation, check_image }
                      })
                    }
                    label="审核封面或缩略图"
                  />
                  <Toggle
                    checked={draft.moderation.check_video}
                    onChange={(check_video) =>
                      setDraft({
                        ...draft,
                        moderation: { ...draft.moderation, check_video }
                      })
                    }
                    label="审核最终视频"
                  />
                </div>
              </SettingsSection>

              <SettingsSection
                icon="settings"
                title="阿里云凭证与服务码"
                description="AccessKey 加密存储且永不回显；任务创建时保存密钥快照，运行中修改不会影响旧任务。"
              >
                <div className="settings-form-grid">
                  <label className="field">
                    <span>Region</span>
                    <input
                      value={draft.moderation.region}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          moderation: {
                            ...draft.moderation,
                            region: event.target.value
                          }
                        })
                      }
                    />
                  </label>
                  <label className="field">
                    <span>文本服务</span>
                    <input
                      value={draft.moderation.text_service}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          moderation: {
                            ...draft.moderation,
                            text_service: event.target.value
                          }
                        })
                      }
                    />
                  </label>
                  <label className="field">
                    <span>图片服务</span>
                    <input
                      value={draft.moderation.image_service}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          moderation: {
                            ...draft.moderation,
                            image_service: event.target.value
                          }
                        })
                      }
                    />
                  </label>
                  <label className="field">
                    <span>视频服务</span>
                    <input
                      value={draft.moderation.video_service}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          moderation: {
                            ...draft.moderation,
                            video_service: event.target.value
                          }
                        })
                      }
                    />
                  </label>
                  <label className="field field-wide">
                    <span>AccessKey ID</span>
                    <input
                      type="password"
                      autoComplete="new-password"
                      value={secrets["aliyun.access_key_id"] ?? ""}
                      onChange={(event) =>
                        setSecrets((current) => ({
                          ...current,
                          "aliyun.access_key_id": event.target.value
                        }))
                      }
                      placeholder={
                        draft.secret_configured["aliyun.access_key_id"]
                          ? "已配置；留空保持不变"
                          : "尚未配置"
                      }
                    />
                  </label>
                  <label className="field field-wide">
                    <span>AccessKey Secret</span>
                    <input
                      type="password"
                      autoComplete="new-password"
                      value={secrets["aliyun.access_key_secret"] ?? ""}
                      onChange={(event) =>
                        setSecrets((current) => ({
                          ...current,
                          "aliyun.access_key_secret": event.target.value
                        }))
                      }
                      placeholder={
                        draft.secret_configured["aliyun.access_key_secret"]
                          ? "已配置；留空保持不变"
                          : "尚未配置"
                      }
                    />
                  </label>
                </div>
                <div className="config-slab-actions">
                  <button
                    className="button button-secondary"
                    type="button"
                    onClick={() => testConnection.mutate("moderation")}
                    disabled={testConnection.isPending}
                  >
                    {testConnection.isPending &&
                    testConnection.variables === "moderation"
                      ? "正在测试…"
                      : "测试内容安全连接"}
                  </button>
                  {testNotice.moderation && <span>{testNotice.moderation}</span>}
                </div>
              </SettingsSection>

              <SettingsSection
                icon="review"
                title="风险处置与超时"
                description="中风险和服务失败默认转人工；高风险默认阻断，绝不会把错误当作通过。"
              >
                <div className="settings-form-grid">
                  {(
                    [
                      ["high_risk_action", "高风险"],
                      ["medium_risk_action", "中风险"],
                      ["failure_action", "服务失败"]
                    ] as const
                  ).map(([key, label]) => (
                    <label className="field" key={key}>
                      <span>{label}</span>
                      <select
                        value={draft.moderation[key]}
                        onChange={(event) =>
                          setDraft({
                            ...draft,
                            moderation: {
                              ...draft.moderation,
                              [key]: event.target.value as
                                | "block"
                                | "manual_review"
                            }
                          })
                        }
                      >
                        <option value="manual_review">转人工审核</option>
                        <option value="block">阻断投稿</option>
                      </select>
                    </label>
                  ))}
                  <label className="field">
                    <span>单次请求超时（秒）</span>
                    <input
                      type="number"
                      min="5"
                      max="300"
                      value={draft.moderation.request_timeout_seconds}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          moderation: {
                            ...draft.moderation,
                            request_timeout_seconds: Number(event.target.value)
                          }
                        })
                      }
                    />
                  </label>
                  <label className="field">
                    <span>视频轮询间隔（秒）</span>
                    <input
                      type="number"
                      min="1"
                      value={draft.moderation.video_poll_seconds}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          moderation: {
                            ...draft.moderation,
                            video_poll_seconds: Number(event.target.value)
                          }
                        })
                      }
                    />
                  </label>
                  <label className="field">
                    <span>视频最大等待（秒）</span>
                    <input
                      type="number"
                      min="30"
                      value={draft.moderation.video_maximum_wait_seconds}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          moderation: {
                            ...draft.moderation,
                            video_maximum_wait_seconds: Number(event.target.value)
                          }
                        })
                      }
                    />
                  </label>
                </div>
              </SettingsSection>
            </>
          )}

          {tab === "publishing" && (
            <>
              <SettingsSection
                icon="route"
                title="发布并发与失败恢复"
                description="AcFun 和 Bilibili 分平台执行，使用幂等指纹、持久化尝试记录和不确定结果回查。"
              >
                <div className="rule-grid">
                  <Toggle
                    checked={draft.publishing.auto_publish_after_review}
                    onChange={(auto_publish_after_review) =>
                      setDraft({
                        ...draft,
                        publishing: {
                          ...draft.publishing,
                          auto_publish_after_review
                        }
                      })
                    }
                    label="允许审核后自动发布"
                    description="只有任务自身也选择了自动投稿策略时才会生效。"
                  />
                  <Toggle
                    checked={draft.publishing.reconcile_uncertain_results}
                    onChange={(reconcile_uncertain_results) =>
                      setDraft({
                        ...draft,
                        publishing: {
                          ...draft.publishing,
                          reconcile_uncertain_results
                        }
                      })
                    }
                    label="自动回查不确定结果"
                    description="上传超时但平台可能已收稿时先回查，避免重复投稿。"
                  />
                  <label className="field">
                    <span>并发上传数</span>
                    <input
                      type="number"
                      min="1"
                      max="32"
                      value={draft.publishing.maximum_concurrent_uploads}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          publishing: {
                            ...draft.publishing,
                            maximum_concurrent_uploads: Number(event.target.value)
                          }
                        })
                      }
                    />
                  </label>
                  <label className="field">
                    <span>最大尝试次数</span>
                    <input
                      type="number"
                      min="1"
                      max="20"
                      value={draft.publishing.maximum_attempts}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          publishing: {
                            ...draft.publishing,
                            maximum_attempts: Number(event.target.value)
                          }
                        })
                      }
                    />
                  </label>
                  <label className="field">
                    <span>重试延迟（秒）</span>
                    <input
                      type="number"
                      min="1"
                      value={draft.publishing.retry_delay_seconds}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          publishing: {
                            ...draft.publishing,
                            retry_delay_seconds: Number(event.target.value)
                          }
                        })
                      }
                    />
                  </label>
                </div>
              </SettingsSection>

              <SettingsSection
                icon="settings"
                title="平台资源与投稿策略"
                description="账号、Cookie 绑定、分区缓存、专用转码预设和投稿模板有独立版本与可用性检查。"
              >
                <div className="settings-link-panel">
                  <div>
                    <strong>AcFun / Bilibili 投稿配置</strong>
                    <p>创建平台账号并验证登录，刷新分区，配置转码预设和审核后人工/自动投稿策略。</p>
                  </div>
                  <Link className="button button-primary" to="/publishing/settings">
                    打开平台投稿配置
                  </Link>
                </div>
              </SettingsSection>
            </>
          )}

          {tab === "youtube" && (
            <>
              <SettingsSection
                icon="monitor"
                title="监控数据源"
                description="连接 YouTube 的官方数据服务，用于频道、关键词和完整剧集的持续发现。"
                help={
                  <HelpLink label="申请与配置" title="如何申请 YouTube 数据密钥">
                    <ol>
                      <li>登录 Google Cloud Console，新建或选择一个项目。</li>
                      <li>在 API 库中启用 YouTube Data API v3。</li>
                      <li>创建 API Key，建议限制为仅允许调用 YouTube Data API。</li>
                      <li>把密钥粘贴到本页，保存后点击“测试监控数据源”。</li>
                    </ol>
                    <div className="guide-links">
                      <ExternalGuideLink href="https://developers.google.com/youtube/v3/getting-started?hl=zh-CN">
                        官方入门说明
                      </ExternalGuideLink>
                      <ExternalGuideLink href="https://developers.google.com/youtube/registering_an_application">
                        官方申请流程
                      </ExternalGuideLink>
                    </div>
                  </HelpLink>
                }
              >
                <div className="mode-selector">
                  <label>
                    <input
                      type="radio"
                      checked={draft.youtube.provider === "google"}
                      onChange={() =>
                        setDraft({
                          ...draft,
                          youtube: { ...draft.youtube, provider: "google" }
                        })
                      }
                    />
                    <span>
                      <strong>Google Data API</strong>
                      <small>用于真实频道与关键词发现，需要 API Key。</small>
                    </span>
                  </label>
                  <label>
                    <input
                      type="radio"
                      checked={draft.youtube.provider === "fixture"}
                      onChange={() =>
                        setDraft({
                          ...draft,
                          youtube: { ...draft.youtube, provider: "fixture" }
                        })
                      }
                    />
                    <span>
                      <strong>演示模式</strong>
                      <small>不连接 YouTube，仅用于本地演示流程。</small>
                    </span>
                  </label>
                </div>
                <div className="settings-form-grid">
                  <label className="field field-wide">
                    <span>数据服务地址（高级）</span>
                    <input
                      value={draft.youtube.api_base_url}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          youtube: {
                            ...draft.youtube,
                            api_base_url: event.target.value
                          }
                        })
                      }
                    />
                  </label>
                  <label className="field field-wide">
                    <span>YouTube Data API Key</span>
                    <input
                      type="password"
                      value={secrets["youtube.api_key"] ?? ""}
                      onChange={(event) =>
                        setSecrets((current) => ({
                          ...current,
                          "youtube.api_key": event.target.value
                        }))
                      }
                      placeholder={
                        draft.secret_configured["youtube.api_key"]
                          ? "已配置；留空保持不变"
                          : "尚未配置"
                      }
                    />
                  </label>
                  <label className="field field-wide">
                    <span>本地验收媒体 URL</span>
                    <input
                      value={draft.youtube.fixture_media_url}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          youtube: {
                            ...draft.youtube,
                            fixture_media_url: event.target.value
                          }
                        })
                      }
                    />
                  </label>
                  <label className="field">
                    <span>请求超时（秒）</span>
                    <input
                      type="number"
                      min="5"
                      max="120"
                      value={draft.youtube.request_timeout_seconds}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          youtube: {
                            ...draft.youtube,
                            request_timeout_seconds: Number(event.target.value)
                          }
                        })
                      }
                    />
                  </label>
                </div>
                <div className="config-slab-actions">
                  <button
                    className="button button-secondary"
                    type="button"
                    onClick={() => testConnection.mutate("youtube")}
                    disabled={testConnection.isPending}
                  >
                    测试监控数据源
                  </button>
                  {testNotice.youtube && <span>{testNotice.youtube}</span>}
                </div>
              </SettingsSection>
              <SettingsSection
                icon="route"
                title="监控数据网络代理"
                description="仅在当前网络无法稳定读取 YouTube 搜索和频道数据时启用。"
                help={
                  <HelpLink title="监控数据网络代理有什么用">
                    <p>它只负责“发现视频”阶段的搜索、频道列表和视频资料读取，不会改变视频文件的下载线路。</p>
                    <p>一般网络可以正常访问 YouTube 数据时请保持关闭；只有监控查询超时、但下载线路另有配置时，才需要单独填写。</p>
                  </HelpLink>
                }
              >
                <Toggle
                  checked={draft.youtube.proxy_enabled}
                  onChange={(proxy_enabled) =>
                    setDraft({
                      ...draft,
                      youtube: { ...draft.youtube, proxy_enabled }
                    })
                  }
                  label="启用监控数据代理"
                />
                <div className="settings-form-grid">
                  <label className="field field-wide">
                    <span>代理 URL</span>
                    <input
                      value={draft.youtube.proxy_url}
                      disabled={!draft.youtube.proxy_enabled}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          youtube: {
                            ...draft.youtube,
                            proxy_url: event.target.value
                          }
                        })
                      }
                      placeholder="http://127.0.0.1:7890"
                    />
                  </label>
                  <label className="field">
                    <span>用户名</span>
                    <input
                      value={draft.youtube.proxy_username}
                      disabled={!draft.youtube.proxy_enabled}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          youtube: {
                            ...draft.youtube,
                            proxy_username: event.target.value
                          }
                        })
                      }
                    />
                  </label>
                  <label className="field">
                    <span>密码</span>
                    <input
                      type="password"
                      disabled={!draft.youtube.proxy_enabled}
                      value={secrets["youtube.proxy_password"] ?? ""}
                      onChange={(event) =>
                        setSecrets((current) => ({
                          ...current,
                          "youtube.proxy_password": event.target.value
                        }))
                      }
                      placeholder={
                        draft.secret_configured["youtube.proxy_password"]
                          ? "已配置；留空保持不变"
                          : "可选"
                      }
                    />
                  </label>
                </div>
              </SettingsSection>
            </>
          )}
        </div>
      </div>
    </>
  );
}
