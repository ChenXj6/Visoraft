import { useMemo, useRef, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, type CookieProfile, type CreateCookieCloudInput } from "../api";
import {
  ConfirmDialog,
  EmptyState,
  LoadingBlock,
  ModalDialog,
  PageHeader,
  QueryError
} from "../components";
import { cookieStatusLabels, formatDateTime } from "../format";
import { Icon } from "../icons";
import { ExternalGuideLink, HelpLink, TransientNotice } from "../product-ui";

type CloudFields = keyof CreateCookieCloudInput;
type ProfileFilter = "all" | "ready" | "attention";

function errorMessage(error: unknown, fallback: string) {
  return error instanceof ApiError ? error.message : fallback;
}

function ProfileStatus({ profile }: { profile: CookieProfile }) {
  return (
    <span className={`cookie-state cookie-state-${profile.status}`}>
      <i aria-hidden="true" />
      {cookieStatusLabels[profile.status] ?? "未知状态"}
    </span>
  );
}

export default function CookieProfilesPage() {
  const queryClient = useQueryClient();
  const fileInput = useRef<HTMLInputElement>(null);
  const [uploadName, setUploadName] = useState("");
  const [uploadFile, setUploadFile] = useState<File>();
  const [uploadError, setUploadError] = useState("");
  const [cloudValues, setCloudValues] = useState<CreateCookieCloudInput>({
    name: "",
    server_url: "",
    uuid: "",
    password: ""
  });
  const [cloudErrors, setCloudErrors] = useState<Partial<Record<CloudFields, string>>>({});
  const [cloudError, setCloudError] = useState("");
  const [cloudOpen, setCloudOpen] = useState(false);
  const [profileFilter, setProfileFilter] = useState<ProfileFilter>("all");
  const [deleteTarget, setDeleteTarget] = useState<CookieProfile>();
  const [profileNotice, setProfileNotice] = useState("");

  const profiles = useQuery({
    queryKey: ["cookie-profiles"],
    queryFn: api.cookieProfiles,
    refetchInterval: 15_000
  });

  const refreshProfiles = async () => {
    await queryClient.invalidateQueries({ queryKey: ["cookie-profiles"] });
  };

  const upload = useMutation({
    mutationFn: ({ name, file }: { name: string; file: File }) =>
      api.uploadCookieProfile(name, file),
    onSuccess: async (profile) => {
      setUploadName("");
      setUploadFile(undefined);
      setUploadError("");
      setProfileNotice(`“${profile.name}”已加密保存，可供任务使用。`);
      if (fileInput.current) fileInput.current.value = "";
      await refreshProfiles();
    },
    onError: (error) => {
      setUploadError(errorMessage(error, "Cookie 文件上传失败"));
    }
  });

  const createCloud = useMutation({
    mutationFn: api.createCookieCloudProfile,
    onSuccess: async (profile) => {
      setCloudValues({ name: "", server_url: "", uuid: "", password: "" });
      setCloudErrors({});
      setCloudError("");
      setCloudOpen(false);
      setProfileNotice(
        profile.has_usable_cookies
          ? `“${profile.name}”同步完成，可供任务使用。`
          : `“${profile.name}”已保存，但本次同步失败，请查看错误后重试。`
      );
      await refreshProfiles();
    },
    onError: (error) => {
      if (error instanceof ApiError && error.fields) {
        setCloudErrors(error.fields as Partial<Record<CloudFields, string>>);
      }
      setCloudError(errorMessage(error, "CookieCloud 配置保存失败"));
    }
  });

  const sync = useMutation({
    mutationFn: api.syncCookieProfile,
    onSuccess: async (profile) => {
      setProfileNotice(
        profile.status === "ready"
          ? `“${profile.name}”同步完成。`
          : `“${profile.name}”同步失败，已保留上一次可用 Cookie。`
      );
      await refreshProfiles();
    },
    onError: (error) => {
      setProfileNotice(errorMessage(error, "CookieCloud 同步失败"));
    }
  });

  const syncAll = useMutation({
    mutationFn: async () => {
      const cloudProfiles = profiles.data?.items.filter((profile) => profile.kind === "cookiecloud") ?? [];
      for (const profile of cloudProfiles) {
        await api.syncCookieProfile(profile.id);
      }
      return cloudProfiles.length;
    },
    onSuccess: async (count) => {
      setProfileNotice(count > 0 ? `已重新校验 ${count} 个自动同步配置。` : "Cookie 状态已刷新。");
      await refreshProfiles();
    },
    onError: (error) => {
      setProfileNotice(errorMessage(error, "部分 Cookie 配置校验失败"));
      void refreshProfiles();
    }
  });

  const remove = useMutation({
    mutationFn: api.deleteCookieProfile,
    onSuccess: async () => {
      setProfileNotice(`“${deleteTarget?.name ?? "Cookie 配置"}”已删除。`);
      setDeleteTarget(undefined);
      await refreshProfiles();
    },
    onError: (error) => {
      setProfileNotice(errorMessage(error, "Cookie 配置删除失败"));
      setDeleteTarget(undefined);
    }
  });

  const readyCount = useMemo(
    () => profiles.data?.items.filter((profile) => profile.has_usable_cookies).length ?? 0,
    [profiles.data]
  );
  const attentionCount = useMemo(
    () => profiles.data?.items.filter((profile) => !profile.has_usable_cookies || profile.status === "error").length ?? 0,
    [profiles.data]
  );
  const attentionProfiles = useMemo(
    () => profiles.data?.items.filter((profile) => !profile.has_usable_cookies || profile.status === "error") ?? [],
    [profiles.data]
  );
  const filteredProfiles = useMemo(() => {
    const items = profiles.data?.items ?? [];
    if (profileFilter === "ready") return items.filter((profile) => profile.has_usable_cookies && profile.status !== "error");
    if (profileFilter === "attention") return items.filter((profile) => !profile.has_usable_cookies || profile.status === "error");
    return items;
  }, [profileFilter, profiles.data]);

  const submitUpload = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setUploadError("");
    if (!uploadFile) {
      setUploadError("请选择 Netscape cookies.txt 文件");
      return;
    }
    upload.mutate({ name: uploadName.trim(), file: uploadFile });
  };

  const submitCloud = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setCloudError("");
    setCloudErrors({});
    createCloud.mutate({
      name: cloudValues.name.trim(),
      server_url: cloudValues.server_url.trim(),
      uuid: cloudValues.uuid.trim(),
      password: cloudValues.password
    });
  };

  const updateCloud = (field: CloudFields, value: string) => {
    setCloudValues((current) => ({ ...current, [field]: value }));
    setCloudErrors((current) => ({ ...current, [field]: undefined }));
  };

  return (
    <>
      <PageHeader title="Cookie 管理" actions={<>
        <button className="button button-secondary button-small" type="button" disabled={syncAll.isPending || profiles.isPending} onClick={() => syncAll.mutate()}>{syncAll.isPending ? "正在校验…" : "全部重新校验"}</button>
        <button className="button button-primary button-small" type="button" onClick={() => fileInput.current?.click()}>导入 Cookie</button>
      </>} />

      <div className="prototype-cookie-layout">
        <section className="work-panel prototype-cookie-add">
          <header className="prototype-cookie-panel-head">
            <h2>添加 Cookie</h2>
            <span>二选一即可</span>
          </header>
          <form className="prototype-cookie-upload" onSubmit={submitUpload}>
            <label className="prototype-cookie-drop">
              <input
                ref={fileInput}
                type="file"
                accept=".txt,text/plain"
                onChange={(event) => {
                  const next = event.target.files?.[0];
                  setUploadFile(next);
                  if (next && !uploadName) setUploadName(next.name.replace(/\.txt$/i, ""));
                  setUploadError("");
                }}
              />
              <span className="prototype-cookie-drop-icon" aria-hidden="true"><Icon name="file" /></span>
              <strong>{uploadFile?.name ?? "上传 cookies.txt"}</strong>
              <small>
                {uploadFile
                  ? `${(uploadFile.size / 1024).toFixed(1)} KiB · 点击可重新选择`
                  : "从浏览器导出 Netscape 格式文件，拖拽到此处或点击选择"}
              </small>
              <span className="button button-secondary button-small">{uploadFile ? "重新选择" : "选择文件"}</span>
            </label>
            {uploadError ? <p className="form-alert" role="alert">{uploadError}</p> : null}
            {uploadFile ? (
              <button className="button button-primary button-small" type="submit" disabled={upload.isPending}>
                {upload.isPending ? "正在校验…" : "上传并保存"}
              </button>
            ) : null}
          </form>
          <div className="prototype-cookie-or"><span>或</span></div>
          <div className="prototype-cookie-cloud-card">
            <span className="prototype-cookie-cloud-icon" aria-hidden="true"><Icon name="cookie" /></span>
            <strong>连接 CookieCloud</strong>
            <small>填入服务器地址与密钥，自动同步并保持更新</small>
            <button className="button button-primary button-small" type="button" onClick={() => setCloudOpen(true)}>
              去连接
            </button>
          </div>
        </section>

        <section className="work-panel prototype-cookie-vault">
          <header className="prototype-cookie-panel-head">
            <h2>已导入 <small>{profiles.data?.items.length ?? 0}</small></h2>
            <div className="prototype-cookie-filters" role="group" aria-label="Cookie 状态筛选">
              <button type="button" className={profileFilter === "all" ? "active" : ""} onClick={() => setProfileFilter("all")}>全部 {profiles.data?.items.length ?? 0}</button>
              <button type="button" className={profileFilter === "ready" ? "active" : ""} onClick={() => setProfileFilter("ready")}>有效 {readyCount}</button>
              <button type="button" className={profileFilter === "attention" ? "active" : ""} onClick={() => setProfileFilter("attention")}>需要更新 {attentionCount}</button>
            </div>
          </header>

      {profileNotice && (
        <TransientNotice
          tone={/失败|错误/.test(profileNotice) ? "error" : "success"}
          onDismiss={() => setProfileNotice("")}
        >
          {profileNotice}
        </TransientNotice>
      )}
        {profiles.isPending ? (
          <LoadingBlock label="正在读取 Cookie 配置" />
        ) : profiles.isError ? (
          <QueryError
            title="Cookie 配置不可用"
            message={profiles.error.message}
            retry={() => void profiles.refetch()}
          />
        ) : filteredProfiles.length === 0 ? (
          <EmptyState
            title={profiles.data.items.length === 0 ? "还没有 Cookie 配置" : "这个筛选下没有配置"}
            description={profiles.data.items.length === 0 ? "上传 cookies.txt 或连接 CookieCloud 后即可在任务中使用。" : "切换状态筛选查看其他配置。"}
          />
        ) : (
          <div className="cookie-profile-list">
            {filteredProfiles.map((profile) => (
              <article className="cookie-profile-row" key={profile.id}>
                <div
                  className="profile-type"
                  aria-label={profile.kind === "cookiecloud" ? "CookieCloud" : "本地文件"}
                >
                  <Icon name={profile.kind === "cookiecloud" ? "cookie" : "shield"} />
                </div>
                <div className="profile-main">
                  <div className="profile-title-line">
                    <h3>{profile.name}</h3>
                    <ProfileStatus profile={profile} />
                  </div>
                  <p>
                    {profile.kind === "cookiecloud"
                      ? `CookieCloud 自动同步 · ${profile.cookie_count} 条 · ${profile.domain_count} 个域名 · ${formatDateTime(profile.last_synced_at)}`
                      : `${profile.source_filename || "cookies.txt"} · ${profile.cookie_count} 条 · ${profile.domain_count} 个域名 · ${formatDateTime(profile.last_synced_at)}`}
                  </p>
                  {profile.last_error && (
                    <p className="profile-error" role="alert">
                      {profile.last_error}
                    </p>
                  )}
                </div>
                <dl className="profile-stats">
                  <div>
                    <dt>条目</dt>
                    <dd>{profile.cookie_count}</dd>
                  </div>
                  <div>
                    <dt>域名</dt>
                    <dd>{profile.domain_count}</dd>
                  </div>
                  <div>
                    <dt>同步时间</dt>
                    <dd>{formatDateTime(profile.last_synced_at)}</dd>
                  </div>
                </dl>
                <div className="profile-actions">
                  {profile.kind === "cookiecloud" && (
                    <button
                      type="button"
                      className="button button-secondary button-small"
                      disabled={sync.isPending}
                      onClick={() => sync.mutate(profile.id)}
                    >
                      {sync.isPending && sync.variables === profile.id ? "同步中…" : "立即同步"}
                    </button>
                  )}
                  <button
                    type="button"
                    className="button button-quiet-danger"
                    onClick={() => setDeleteTarget(profile)}
                  >
                    删除
                  </button>
                </div>
              </article>
            ))}
          </div>
        )}
      </section>
      </div>

      {attentionProfiles.length > 0 ? (
        <div className="warn-banner prototype-cookie-warning" role="status">
          <Icon name="shield" />
          <div>
            <strong>{attentionProfiles[0]?.name ?? "Cookie 配置"} 需要更新</strong>
            <span>该登录配置当前不可用，重新导入或同步后，关联任务可继续处理。</span>
          </div>
        </div>
      ) : null}

      <ModalDialog
        open={cloudOpen}
        title="连接 CookieCloud"
        description="填入服务器地址与加密密钥"
        icon="cookie"
        tone="success"
        closeDisabled={createCloud.isPending}
        onClose={() => {
          if (!createCloud.isPending) {
            setCloudOpen(false);
            setCloudError("");
          }
        }}
        footer={
          <>
            <button className="button button-secondary button-small" type="button" disabled={createCloud.isPending} onClick={() => setCloudOpen(false)}>取消</button>
            <button className="button button-primary button-small" type="submit" form="cookiecloud-connect-form" disabled={createCloud.isPending}>
              {createCloud.isPending ? "正在连接…" : "连接"}
            </button>
          </>
        }
      >
        <form id="cookiecloud-connect-form" className="prototype-cookiecloud-form" onSubmit={submitCloud} noValidate>
          <label className="field">
            <span>配置名称</span>
            <input value={cloudValues.name} onChange={(event) => updateCloud("name", event.target.value)} placeholder="例如：Bilibili 主号" maxLength={80} aria-invalid={Boolean(cloudErrors.name)} />
            {cloudErrors.name ? <small className="field-error">{cloudErrors.name}</small> : null}
          </label>
          <label className="field">
            <span>服务器地址</span>
            <input type="url" value={cloudValues.server_url} onChange={(event) => updateCloud("server_url", event.target.value)} placeholder="https://cookiecloud.example.com" autoComplete="url" aria-invalid={Boolean(cloudErrors.server_url)} />
            <small className="field-help">支持自建服务，建议使用 HTTPS。</small>
            {cloudErrors.server_url ? <small className="field-error">{cloudErrors.server_url}</small> : null}
          </label>
          <label className="field">
            <span>用户标识（UUID）</span>
            <input value={cloudValues.uuid} onChange={(event) => updateCloud("uuid", event.target.value)} autoComplete="off" spellCheck={false} aria-invalid={Boolean(cloudErrors.uuid)} />
            {cloudErrors.uuid ? <small className="field-error">{cloudErrors.uuid}</small> : null}
          </label>
          <label className="field">
            <span>加密密码</span>
            <input type="password" value={cloudValues.password} onChange={(event) => updateCloud("password", event.target.value)} autoComplete="new-password" aria-invalid={Boolean(cloudErrors.password)} />
            <small className="field-help">仅在本机加密保存。</small>
            {cloudErrors.password ? <small className="field-error">{cloudErrors.password}</small> : null}
          </label>
          {cloudError ? <p className="form-alert" role="alert">{cloudError}</p> : null}
          <HelpLink label="查看安装与配置流程" title="如何配置 CookieCloud">
            <ol>
              <li>部署或准备自己的 CookieCloud 服务。</li>
              <li>在浏览器扩展中填写相同的服务地址、用户标识和加密密码。</li>
              <li>先从扩展同步，再在此连接。</li>
            </ol>
            <div className="guide-links">
              <ExternalGuideLink href="https://github.com/easychen/CookieCloud">项目与部署说明</ExternalGuideLink>
              <ExternalGuideLink href="https://chromewebstore.google.com/detail/cookiecloud/ffjiejobkoibkjlhjnlgmcnnigeelbdl?hl=en">Chrome 浏览器扩展</ExternalGuideLink>
            </div>
          </HelpLink>
        </form>
      </ModalDialog>

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        title="确认删除 Cookie？"
        description="删除后关联任务需重新选择登录配置"
        confirmLabel="删除"
        destructive
        busy={remove.isPending}
        onConfirm={() => {
          if (deleteTarget) remove.mutate(deleteTarget.id);
        }}
        onClose={() => {
          if (!remove.isPending) setDeleteTarget(undefined);
        }}
      >
        {deleteTarget ? (
          <div className="cookie-delete-summary">
            <strong>{deleteTarget.name}</strong>
            <span>
              {deleteTarget.kind === "cookiecloud" ? "CookieCloud 自动同步" : "本地 Cookie 文件"}
              {` · ${deleteTarget.cookie_count} 条 Cookie · 上次校验 ${formatDateTime(deleteTarget.last_synced_at)}`}
            </span>
          </div>
        ) : null}
      </ConfirmDialog>
    </>
  );
}
