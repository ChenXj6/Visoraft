import { useMemo, useRef, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, type CookieProfile, type CreateCookieCloudInput } from "../api";
import {
  ConfirmDialog,
  EmptyState,
  LoadingBlock,
  PageHeader,
  QueryError
} from "../components";
import { cookieStatusLabels, formatDateTime } from "../format";
import { Icon } from "../icons";
import { ExternalGuideLink, HelpLink, TransientNotice } from "../product-ui";

type CloudFields = keyof CreateCookieCloudInput;

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
      <PageHeader
        title="登录凭据工作台"
        description="上传浏览器导出的 cookies.txt，或连接自己的 CookieCloud。任务需要登录时会安全读取对应配置。"
        actions={
          <div className="cookie-count-block" aria-label={`${readyCount} 个可用 Cookie 配置`}>
            <strong>{readyCount}</strong>
            <span>可用</span>
          </div>
        }
      />

      <div className="cookie-security-note" role="note">
        <span className="security-note-mark" aria-hidden="true">
          <Icon name="shield" />
        </span>
        <div>
          <strong>Cookie 按敏感凭据处理</strong>
          <p>
            登录信息会加密保存；CookieCloud 密码只用于本机完成同步。
            同步成功代表配置可读取，但网站登录状态仍可能过期，请留意校验结果。
          </p>
        </div>
      </div>

      <div className="cookie-input-grid">
        <form className="work-panel cookie-input-panel" onSubmit={submitUpload}>
          <header className="section-heading">
            <span className="sequence-mark"><Icon name="shield" /></span>
            <div>
              <p className="eyebrow">本地导入</p>
              <h2>上传 cookies.txt</h2>
              <p>适合从浏览器导出的 Netscape 格式文件，最大 5 MiB。</p>
            </div>
          </header>

          <label className="field">
            <span>配置名称</span>
            <input
              value={uploadName}
              onChange={(event) => setUploadName(event.target.value)}
              placeholder="例如：YouTube 主账号"
              maxLength={80}
              autoComplete="off"
            />
            <small className="field-help">留空时使用文件名。</small>
          </label>

          <label className="file-drop">
            <input
              ref={fileInput}
              type="file"
              accept=".txt,text/plain"
              onChange={(event) => {
                setUploadFile(event.target.files?.[0]);
                setUploadError("");
              }}
            />
            <span className="file-drop-code" aria-hidden="true">
              TXT
            </span>
            <span>
              <strong>{uploadFile?.name ?? "选择 cookies.txt"}</strong>
              <small>
                {uploadFile
                  ? `${(uploadFile.size / 1024).toFixed(1)} KiB`
                  : "点击选择浏览器导出的 Netscape 文件"}
              </small>
            </span>
          </label>

          {uploadError && (
            <p className="form-alert" role="alert">
              {uploadError}
            </p>
          )}
          <button
            className="button button-primary button-block"
            type="submit"
            disabled={upload.isPending}
          >
            {upload.isPending ? "正在校验并加密…" : "上传并保存"}
          </button>
        </form>

        <form className="work-panel cookie-input-panel" onSubmit={submitCloud} noValidate>
          <header className="section-heading">
            <span className="sequence-mark"><Icon name="cookie" /></span>
            <div>
              <p className="eyebrow">自动同步</p>
              <div className="section-heading-tools">
                <h2>连接 CookieCloud</h2>
                <HelpLink label="安装与配置" title="如何配置 CookieCloud">
                  <ol>
                    <li>部署或准备一个自己的 CookieCloud 服务地址。</li>
                    <li>在浏览器安装 CookieCloud 扩展，填写同一服务地址、用户标识和端到端密码。</li>
                    <li>先在浏览器扩展中同步，再把同一组信息填入本页并保存。</li>
                    <li>同步成功后，可在新建任务时选择这条登录配置。</li>
                  </ol>
                  <div className="guide-links">
                    <ExternalGuideLink href="https://github.com/easychen/CookieCloud">
                      CookieCloud 项目与部署说明
                    </ExternalGuideLink>
                    <ExternalGuideLink href="https://chromewebstore.google.com/detail/cookiecloud/ffjiejobkoibkjlhjnlgmcnnigeelbdl?hl=en">
                      Chrome 浏览器扩展
                    </ExternalGuideLink>
                  </div>
                </HelpLink>
              </div>
              <p>填写你自己的 CookieCloud 地址；保存时立即执行一次同步。</p>
            </div>
          </header>

          <div className="field-pair">
            <label className="field">
              <span>配置名称</span>
              <input
                value={cloudValues.name}
                onChange={(event) => updateCloud("name", event.target.value)}
                placeholder="例如：CookieCloud / YouTube"
                maxLength={80}
                aria-invalid={Boolean(cloudErrors.name)}
              />
              {cloudErrors.name && <small className="field-error">{cloudErrors.name}</small>}
            </label>
            <label className="field">
              <span>服务地址</span>
              <input
                type="url"
                value={cloudValues.server_url}
                onChange={(event) => updateCloud("server_url", event.target.value)}
                placeholder="https://cookie.example.com"
                autoComplete="url"
                aria-invalid={Boolean(cloudErrors.server_url)}
              />
              {cloudErrors.server_url && (
                <small className="field-error">{cloudErrors.server_url}</small>
              )}
            </label>
          </div>

          <div className="field-pair">
            <label className="field">
              <span>UUID</span>
              <input
                value={cloudValues.uuid}
                onChange={(event) => updateCloud("uuid", event.target.value)}
                autoComplete="off"
                spellCheck={false}
                aria-invalid={Boolean(cloudErrors.uuid)}
              />
              {cloudErrors.uuid && <small className="field-error">{cloudErrors.uuid}</small>}
            </label>
            <label className="field">
              <span>端到端加密密码</span>
              <input
                type="password"
                value={cloudValues.password}
                onChange={(event) => updateCloud("password", event.target.value)}
                autoComplete="new-password"
                aria-invalid={Boolean(cloudErrors.password)}
              />
              {cloudErrors.password && (
                <small className="field-error">{cloudErrors.password}</small>
              )}
            </label>
          </div>

          {cloudError && (
            <p className="form-alert" role="alert">
              {cloudError}
            </p>
          )}
          <button
            className="button button-primary button-block"
            type="submit"
            disabled={createCloud.isPending}
          >
            {createCloud.isPending ? "正在连接并解密…" : "保存并同步"}
          </button>
        </form>
      </div>

      {profileNotice && (
        <TransientNotice
          tone={/失败|错误/.test(profileNotice) ? "error" : "success"}
          onDismiss={() => setProfileNotice("")}
        >
          {profileNotice}
        </TransientNotice>
      )}

      <section className="work-panel cookie-vault">
        <header className="work-panel-head">
          <div>
            <p className="eyebrow">已保存配置</p>
            <h2>Cookie 配置</h2>
          </div>
          <button
            className="icon-button"
            type="button"
            aria-label="刷新 Cookie 配置"
            onClick={() => void profiles.refetch()}
          >
            <Icon name="refresh" />
          </button>
        </header>

        {profiles.isPending ? (
          <LoadingBlock label="正在读取 Cookie 配置" />
        ) : profiles.isError ? (
          <QueryError
            title="Cookie 配置不可用"
            message={profiles.error.message}
            retry={() => void profiles.refetch()}
          />
        ) : profiles.data.items.length === 0 ? (
          <EmptyState
            title="还没有 Cookie 配置"
            description="上传 cookies.txt 或连接 CookieCloud 后，需要登录的网站任务就能选择对应配置。"
          />
        ) : (
          <div className="cookie-profile-list">
            {profiles.data.items.map((profile) => (
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
                      ? profile.server_url
                      : profile.source_filename || "上传文件"}
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
                      className="button button-secondary"
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

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        title={`删除“${deleteTarget?.name ?? "Cookie 配置"}”？`}
        description="密文 Cookie 和 CookieCloud 凭据会从数据库删除。已关联任务不会删除，但下次重试前需要改选其他配置。"
        confirmLabel="确认删除"
        destructive
        busy={remove.isPending}
        onConfirm={() => {
          if (deleteTarget) remove.mutate(deleteTarget.id);
        }}
        onClose={() => {
          if (!remove.isPending) setDeleteTarget(undefined);
        }}
      />
    </>
  );
}
