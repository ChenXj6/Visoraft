import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import { Link, useNavigate } from "react-router-dom";
import { z } from "zod";
import { api, ApiError, type CreateTaskInput, type Platform } from "../api";
import { PageHeader } from "../components";

const formSchema = z.object({
  source_url: z.url("请输入有效的视频 URL"),
  target_platforms: z.array(z.enum(["acfun", "bilibili"])).min(1, "至少选择一个目标平台"),
  cookie_profile_id: z.string(),
  repost_statement_version: z.enum(["brief_v1", "full_v1"]),
  posting_strategy_id: z.string(),
  auto_publish: z.boolean()
});

type FormValues = z.infer<typeof formSchema>;

export default function NewTaskPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [submitError, setSubmitError] = useState("");
  const [step, setStep] = useState(1);
  const cookies = useQuery({
    queryKey: ["cookie-profiles"],
    queryFn: api.cookieProfiles
  });
  const strategies = useQuery({
    queryKey: ["posting-strategies"],
    queryFn: api.postingStrategies
  });
  const {
    register,
    handleSubmit,
    setError,
    setValue,
    watch,
    formState: { errors }
  } = useForm<FormValues>({
    defaultValues: {
      source_url: "",
      target_platforms: ["acfun"],
      cookie_profile_id: "",
      repost_statement_version: "full_v1",
      posting_strategy_id: "",
      auto_publish: false
    }
  });

  const sourceURL = watch("source_url");
  const targetPlatforms = watch("target_platforms");
  const statementVersion = watch("repost_statement_version");
  const postingStrategyID = watch("posting_strategy_id");
  const autoPublish = watch("auto_publish");
  const usableCookies = cookies.data?.items.filter((profile) => profile.has_usable_cookies) ?? [];
  const usableStrategies =
    strategies.data?.items.filter((strategy) => strategy.enabled) ?? [];
  const selectedStrategy = usableStrategies.find(
    (strategy) => strategy.id === postingStrategyID
  );
  const statementPreview =
    statementVersion === "brief_v1"
      ? `转载来源：${sourceURL || "视频 URL"}`
      : `【转载说明】本内容转载自：${sourceURL || "视频 URL"}。转载声明仅说明来源，不代表取得版权许可。`;

  const createTask = useMutation({
    mutationFn: api.createTask,
    onSuccess: async (task) => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["tasks"] }),
        queryClient.invalidateQueries({ queryKey: ["dashboard"] })
      ]);
      navigate(`/tasks/${task.id}`);
    }
  });

  const onSubmit = handleSubmit(async (rawValues) => {
    setSubmitError("");
    const parsed = formSchema.safeParse(rawValues);
    if (!parsed.success) {
      for (const issue of parsed.error.issues) {
        const field = issue.path[0];
        if (typeof field === "string") {
          setError(field as keyof FormValues, { type: "manual", message: issue.message });
        }
      }
      return;
    }

    const input: CreateTaskInput = {
      source_url: parsed.data.source_url,
      target_platforms: parsed.data.target_platforms as Platform[],
      repost_statement_version: parsed.data.repost_statement_version,
      auto_publish: parsed.data.auto_publish,
      ...(parsed.data.cookie_profile_id
        ? { cookie_profile_id: parsed.data.cookie_profile_id }
        : {}),
      ...(parsed.data.posting_strategy_id
        ? { posting_strategy_id: parsed.data.posting_strategy_id }
        : {})
    };

    try {
      await createTask.mutateAsync(input);
    } catch (error) {
      if (error instanceof ApiError) {
        if (error.fields) {
          for (const [field, message] of Object.entries(error.fields)) {
            if (field in rawValues) {
              setError(field as keyof FormValues, { type: "server", message });
            }
          }
        }
        setSubmitError(error.message);
      } else {
      setSubmitError("暂时无法连接任务服务，请确认本地服务已启动。");
      }
    }
  });

  return (
    <>
      <PageHeader
        title="把视频送入处理轨道"
      />

      <form className="new-task-workbench" onSubmit={onSubmit} noValidate>
        <ol className="new-task-steps" aria-label="新建任务步骤">
          {["视频来源", "发布目标", "投稿策略", "确认创建"].map((label, index) => {
            const number = index + 1;
            return (
              <li className={number === step ? "is-active" : number < step ? "is-done" : ""} key={label}>
                <button
                  type="button"
                  onClick={() => {
                    setStep(number);
                    document.getElementById(`new-task-step-${number}`)?.scrollIntoView({ behavior: "smooth", block: "start" });
                  }}
                >
                  <span>{number < step ? "✓" : number}</span>
                  <strong>{label}</strong>
                </button>
              </li>
            );
          })}
        </ol>
        <div className="new-task-main">
          <section id="new-task-step-1" className="work-panel ingest-section wizard-step-panel">
            <header className="section-heading prototype-numbered-heading">
              <div>
                <h2>① 视频地址</h2>
                <p>粘贴常见视频页面或媒体直链；一次任务处理一条视频。</p>
              </div>
              {sourceURL ? <span className="settings-cat-status status-ok">已填写</span> : null}
            </header>
            <label className="field field-prominent">
              <span>视频 URL</span>
              <input
                type="url"
                placeholder="https://www.youtube.com/watch?v=..."
                autoComplete="url"
                autoFocus
                {...register("source_url")}
                aria-invalid={Boolean(errors.source_url)}
              />
              <small className="field-help">支持 YouTube 视频页面链接或媒体直链</small>
              {errors.source_url && (
                <small className="field-error">{errors.source_url.message}</small>
              )}
            </label>
          </section>

          <section id="new-task-step-2" className="work-panel ingest-section wizard-step-panel">
            <header className="section-heading prototype-numbered-heading">
              <div>
                <h2>② 发布目标</h2>
                <p>选择要投稿到的平台。这里只决定后续进入哪些平台流程，不会立即发布。</p>
              </div>
            </header>
            <fieldset className="field">
              <legend className="visually-hidden">目标平台</legend>
              <div className="platform-choices">
                <label className={`platform-choice ${targetPlatforms.includes("acfun") ? "selected" : ""}`}>
                  <input type="checkbox" value="acfun" {...register("target_platforms")} />
                  <span className="platform-choice-code ac">AC</span>
                  <span className="platform-choice-info">
                    <strong>AcFun</strong>
                    <small>进入 AcFun 发布轨道</small>
                  </span>
                </label>
                <label className={`platform-choice ${targetPlatforms.includes("bilibili") ? "selected" : ""}`}>
                  <input type="checkbox" value="bilibili" {...register("target_platforms")} />
                  <span className="platform-choice-code bi">BI</span>
                  <span className="platform-choice-info">
                    <strong>Bilibili</strong>
                    <small>进入 Bilibili 发布轨道</small>
                  </span>
                </label>
              </div>
              {errors.target_platforms && (
                <small className="field-error">{errors.target_platforms.message}</small>
              )}
            </fieldset>
          </section>

          <section id="new-task-step-3" className="work-panel ingest-section wizard-step-panel">
            <header className="section-heading prototype-numbered-heading">
              <div>
                <h2>③ 投稿策略与自动化</h2>
                <p>策略会连同账号、分区、转码预设和审核要求冻结到本任务中。</p>
              </div>
            </header>
            <div className="settings-form-grid">
              <label className="field">
                <span>投稿策略（可选）</span>
                <select
                  {...register("posting_strategy_id", {
                    onChange: (event) => {
                      const strategy = usableStrategies.find((item) => item.id === event.target.value);
                      if (!strategy) {
                        setValue("auto_publish", false);
                        return;
                      }
                      setValue("target_platforms", strategy.target_platforms, { shouldValidate: true });
                      setValue("repost_statement_version", strategy.repost_statement_version);
                      if (strategy.automation_mode !== "automatic_after_review") setValue("auto_publish", false);
                    }
                  })}
                  disabled={strategies.isPending}
                >
                  <option value="">不使用策略，审核后人工配置投稿</option>
                  {usableStrategies.map((strategy) => (
                    <option value={strategy.id} key={strategy.id}>
                      {strategy.name} · {strategy.automation_mode === "automatic_after_review" ? "审核后自动投稿" : "审核后人工确认"}
                    </option>
                  ))}
                </select>
                <small className="field-help">
                  {strategies.isError
                    ? "投稿策略暂时无法读取。"
                    : usableStrategies.length === 0
                      ? "还没有可用策略，可先到投稿配置创建。"
                      : selectedStrategy
                        ? `目标：${selectedStrategy.target_platforms.join("、")}；${selectedStrategy.require_content_moderation ? "要求内容安全审核" : "不强制内容安全审核"}`
                        : "不选策略时，审核通过后会进入投稿工作台人工补齐。"}
                  <Link to="/settings?section=publishing"> 管理投稿配置</Link>
                </small>
                {errors.posting_strategy_id && <small className="field-error">{errors.posting_strategy_id.message}</small>}
              </label>

              <label className="field">
                <span>登录 Cookie（可选）</span>
                <select {...register("cookie_profile_id")} disabled={cookies.isPending}>
                  <option value="">不使用 Cookie</option>
                  {usableCookies.map((profile) => (
                    <option value={profile.id} key={profile.id}>{profile.name} · {profile.cookie_count} 条</option>
                  ))}
                </select>
                <small className="field-help">
                  {cookies.isError ? "Cookie 配置暂时无法读取。" : usableCookies.length === 0 ? "还没有可用配置。" : "公开视频可以不选。"}
                  <Link to="/cookies"> 管理 Cookie</Link>
                </small>
                {errors.cookie_profile_id && <small className="field-error">{errors.cookie_profile_id.message}</small>}
              </label>

              <label className="check-card prototype-auto-publish-card">
                <input
                  type="checkbox"
                  {...register("auto_publish")}
                  disabled={!selectedStrategy || selectedStrategy.automation_mode !== "automatic_after_review"}
                />
                <span aria-hidden="true" />
                <div>
                  <strong>审核通过后自动投稿</strong>
                  <small>仅“审核后自动投稿”策略可开启；自动审核和人工审核通过后都走同一可靠发布队列。</small>
                </div>
              </label>
              {errors.auto_publish && <small className="field-error">{errors.auto_publish.message}</small>}
            </div>
          </section>

        </div>

        <aside className="new-task-side">
          <section className="work-panel statement-editor">
            <header className="section-heading section-heading-compact">
              <div>
                <h2>自动转载声明</h2>
              </div>
            </header>
            <div className="statement-switch">
              <label>
                <input
                  type="radio"
                  value="brief_v1"
                  {...register("repost_statement_version")}
                />
                <span>简版</span>
              </label>
              <label>
                <input
                  type="radio"
                  value="full_v1"
                  {...register("repost_statement_version")}
                />
                <span>完整版</span>
              </label>
            </div>
            <div className="statement-preview">
              <span>预览</span>
              <p>{statementPreview}</p>
            </div>
            <p className="statement-boundary">
              转载声明只是发布文案，不等于版权许可。
            </p>
          </section>

          <section id="new-task-step-4" className="submit-console">
            <dl>
              <div>
                <dt>入队方式</dt>
                <dd>{autoPublish ? "审核后自动投稿" : "立即开始处理"}</dd>
              </div>
              <div>
                <dt>任务状态</dt>
                <dd>持久化</dd>
              </div>
              <div>
                <dt>失败恢复</dt>
                <dd>可重试</dd>
              </div>
            </dl>
            {submitError && (
              <div className="submit-error" role="alert">
                <strong>任务未创建</strong>
                <span>{submitError}</span>
              </div>
            )}
            <button
              className="button button-primary button-block button-large"
              type="submit"
              disabled={createTask.isPending}
            >
              {createTask.isPending ? "正在创建…" : "创建并开始处理"}
            </button>
          <p>提交后任务会安全保存并进入处理队列，关闭页面也不会丢失。</p>
          </section>
        </aside>

      </form>
    </>
  );
}
