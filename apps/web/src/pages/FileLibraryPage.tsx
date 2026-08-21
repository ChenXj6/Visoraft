import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, ApiError, type LocalLibraryAsset } from "../api";
import { ConfirmDialog, EmptyState, LoadingBlock, QueryError, SideDrawer } from "../components";
import { assetKindLabel, formatBytes, formatDateTime } from "../format";
import { Icon } from "../icons";
import { TransientNotice } from "../product-ui";

function fileExtension(asset: LocalLibraryAsset) {
  const dot = asset.original_name.lastIndexOf(".");
  return dot >= 0 ? asset.original_name.slice(dot + 1).toUpperCase() : "文件";
}

function isViewable(asset: LocalLibraryAsset) {
  return asset.content_type.startsWith("video/") ||
    asset.content_type.startsWith("audio/") ||
    asset.content_type.startsWith("image/") ||
    asset.content_type.startsWith("text/") ||
    asset.content_type === "application/json";
}

function localStatusLabel(asset: LocalLibraryAsset) {
  switch (asset.local_status) {
    case "available": return "已存本地";
    case "syncing": return "正在同步";
    case "missing": return "本地缺失";
    case "removed": return "已从本地删除";
    case "error": return "同步失败";
    default: return "等待同步";
  }
}

export default function FileLibraryPage() {
  const queryClient = useQueryClient();
  const searchRef = useRef<HTMLInputElement>(null);
  const [query, setQuery] = useState("");
  const [selectedKey, setSelectedKey] = useState("");
  const [expandedTaskId, setExpandedTaskId] = useState("");
  const [notice, setNotice] = useState("");
  const [pendingDelete, setPendingDelete] = useState<LocalLibraryAsset | null>(null);
  const [storageOpen, setStorageOpen] = useState(false);
  const library = useQuery({
    queryKey: ["files"],
    queryFn: api.files,
    refetchInterval: 10_000
  });

  useEffect(() => {
    const collections = library.data?.collections ?? [];
    const first = collections[0];
    if (first && !collections.some((item) => item.key === selectedKey)) {
      setSelectedKey(first.key);
    }
  }, [library.data?.collections, selectedKey]);

  useEffect(() => {
    const focusFileSearch = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "f") {
        event.preventDefault();
        searchRef.current?.focus();
      }
    };
    window.addEventListener("keydown", focusFileSearch);
    return () => window.removeEventListener("keydown", focusFileSearch);
  }, []);

  const syncFile = useMutation({
    mutationFn: api.syncLocalFile,
    onSuccess: async () => {
      setNotice("文件已开始同步到本地媒体库");
      await queryClient.invalidateQueries({ queryKey: ["files"] });
    },
    onError: (error) => setNotice(error instanceof ApiError ? error.message : "文件同步失败")
  });

  const removeFile = useMutation({
    mutationFn: api.deleteLocalFile,
    onSuccess: async () => {
      setPendingDelete(null);
      setNotice("本地副本已删除，任务和系统原文件仍保留");
      await queryClient.invalidateQueries({ queryKey: ["files"] });
    },
    onError: (error) => setNotice(error instanceof ApiError ? error.message : "删除本地副本失败")
  });

  const selected = useMemo(() => {
    const collection = library.data?.collections.find((item) => item.key === selectedKey);
    if (!collection) return undefined;
    const normalized = query.trim().toLocaleLowerCase();
    if (!normalized) return collection;
    return {
      ...collection,
      folders: collection.folders
        .map((folder) => ({
          ...folder,
          files: folder.files.filter((file) =>
            `${folder.title} ${file.original_name} ${assetKindLabel(file.kind)} ${folder.series_scope ?? ""}`
              .toLocaleLowerCase()
              .includes(normalized)
          )
        }))
        .filter((folder) => folder.files.length > 0)
    };
  }, [library.data?.collections, query, selectedKey]);
  const activeFolder = selected?.folders.find((folder) => folder.task_id === expandedTaskId) ?? selected?.folders[0];

  useEffect(() => {
    const firstFolder = selected?.folders[0];
    if (firstFolder && !selected?.folders.some((folder) => folder.task_id === expandedTaskId)) {
      setExpandedTaskId(firstFolder.task_id);
    }
  }, [expandedTaskId, selected]);

  return (
    <div className="file-browser-page">
      <h1 className="sr-only">本地文件</h1>
      <section className="prototype-file-page-toolbar" aria-label="文件搜索与操作">
        <label className="prototype-file-search">
          <Icon name="search" />
          <input ref={searchRef} type="search" value={query} placeholder="搜索文件名…" onChange={(event) => setQuery(event.target.value)} />
          <kbd>Ctrl F</kbd>
        </label>
        <div>
          <button className="button button-secondary button-small" type="button" disabled={!library.data?.settings.host_path} title={library.data?.settings.host_path} onClick={() => setStorageOpen(true)}>查看本地位置</button>
          <button className="button button-primary button-small" type="button" disabled={library.isFetching} onClick={() => void library.refetch()}>{library.isFetching ? "扫描中" : "重新扫描"}</button>
          <Link className="button button-secondary button-small" to="/settings?section=library">存储设置</Link>
        </div>
      </section>

      {notice && (
        <TransientNotice tone={/失败/.test(notice) ? "error" : "success"} onDismiss={() => setNotice("")}>
          {notice}
        </TransientNotice>
      )}

      {library.isPending ? (
        <LoadingBlock label="正在读取本地媒体库" />
      ) : library.isError ? (
        <QueryError title="本地文件暂时不可用" message={library.error.message} retry={() => void library.refetch()} />
      ) : library.data ? (
        <>
          {library.data.settings.restart_required && (
            <section className="local-path-pending" role="status">
              新位置已保存，运行 <code>.\scripts\local.ps1 storage</code> 后生效。
            </section>
          )}

          {library.data.file_count === 0 ? (
            <EmptyState
              title="还没有可整理的文件"
              description="任务生成媒体文件后，会自动同步到上面的本地目录。"
              action={<Link className="button button-primary" to="/tasks/new">新建任务</Link>}
            />
          ) : (
            <div className="local-library-layout prototype-file-browser">
              <aside className="local-collections" aria-label="文件集合">
                <div className="local-collection-heading"><strong>文件集合</strong><span>{library.data.collection_count}</span></div>
                {library.data.collections.map((collection) => (
                  <button
                    type="button"
                    className={selectedKey === collection.key ? "is-active" : ""}
                    onClick={() => {
                      setSelectedKey(collection.key);
                      setExpandedTaskId("");
                    }}
                    key={collection.key}
                  >
                    <span className="local-collection-icon"><Icon name={collection.kind === "monitor" ? "monitor" : "folder"} /></span>
                    <span>
                      <strong title={collection.title}>{collection.title}</strong>
                      <small>{collection.folder_count} {collection.kind === "monitor" ? "集" : "个任务"} · {collection.file_count} 个文件</small>
                    </span>
                  </button>
                ))}
              </aside>

              <aside className="prototype-folder-tree work-panel" aria-label="任务文件夹">
                <header><strong>文件夹</strong><span>{selected?.folder_count ?? 0}</span></header>
                {!selected || selected.folders.length === 0 ? (
                  <p>没有匹配文件夹</p>
                ) : selected.folders.map((folder) => (
                  <button
                    type="button"
                    className={activeFolder?.task_id === folder.task_id ? "is-active" : ""}
                    onClick={() => setExpandedTaskId(folder.task_id)}
                    key={folder.task_id}
                  >
                    <Icon name="folder" />
                    <span>
                      <strong>{folder.episode_number ? `第 ${folder.episode_number} 集` : "单条任务"}</strong>
                      <small title={folder.title}>{folder.title}</small>
                    </span>
                    <em>{folder.file_count}</em>
                  </button>
                ))}
              </aside>

              <section className="local-library-main prototype-file-stage">
                <header className="local-library-toolbar">
                  <div className="local-library-title">
                    <strong>全部文件 · {selected?.title}{activeFolder?.episode_number ? ` / 第 ${activeFolder.episode_number} 集` : ""}</strong>
                  </div>
                  {activeFolder ? <span className="local-library-summary">{activeFolder.file_count} 个文件 · {formatBytes(activeFolder.local_bytes)}</span> : null}
                </header>

                {!activeFolder ? (
                  <EmptyState title="没有匹配文件" description="请调整搜索内容后重试。" />
                ) : (
                  <>
                    <div className="prototype-file-grid" role="list">
                      {activeFolder.files.map((file) => {
                        const busy = syncFile.isPending || removeFile.isPending;
                        const canonicalAvailable = file.asset_status === "available" && !file.asset_deleted_at;
                        const contentURL = api.assetContentURL(activeFolder.task_id, file.id);
                        return (
                          <article className="prototype-file-tile" role="listitem" key={file.id}>
                            <div className="prototype-file-preview">
                              {file.content_type.startsWith("image/") && canonicalAvailable ? (
                                <img src={contentURL} alt="" loading="lazy" />
                              ) : file.content_type.startsWith("video/") && canonicalAvailable ? (
                                <video src={contentURL} muted preload="metadata" />
                              ) : (
                                <Icon name={file.content_type.startsWith("video/") ? "media" : "file"} />
                              )}
                              <span>{fileExtension(file)}</span>
                            </div>
                            <div className="prototype-file-copy">
                              <strong title={file.original_name}>{file.original_name || assetKindLabel(file.kind)}</strong>
                              <div className="prototype-file-meta">
                                <span className={`local-file-status is-${file.local_status}`}>{localStatusLabel(file)}</span>
                                <small>{formatBytes(file.size_bytes)}</small>
                              </div>
                            </div>
                            <div className="prototype-file-actions">
                              {canonicalAvailable && isViewable(file) ? <a href={contentURL} target="_blank" rel="noreferrer">查看</a> : null}
                              {file.local_status === "available" ? (
                                <button type="button" disabled={busy} onClick={() => setPendingDelete(file)}>删除本地</button>
                              ) : canonicalAvailable ? (
                                <button type="button" disabled={busy || file.local_status === "syncing"} onClick={() => syncFile.mutate(file.id)}>
                                  {file.local_status === "syncing" ? "同步中" : "同步到本地"}
                                </button>
                              ) : null}
                            </div>
                          </article>
                        );
                      })}
                    </div>
                  </>
                )}
              </section>
            </div>
          )}
        </>
      ) : null}

      <ConfirmDialog
        open={Boolean(pendingDelete)}
        title="删除本地副本？"
        description={`“${pendingDelete?.original_name ?? "该文件"}”只会从电脑上的媒体库移除，任务记录和系统原文件仍然保留。`}
        confirmLabel="删除本地副本"
        destructive
        busy={removeFile.isPending}
        onClose={() => setPendingDelete(null)}
        onConfirm={() => pendingDelete && removeFile.mutate(pendingDelete.id)}
      />
      <SideDrawer
        open={storageOpen}
        title="本地存储位置"
        onClose={() => setStorageOpen(false)}
        footer={
          <>
            <button className="button button-secondary button-small" type="button" onClick={() => setStorageOpen(false)}>
              关闭
            </button>
            <Link className="button button-primary button-small" to="/settings?section=library" onClick={() => setStorageOpen(false)}>
              存储设置
            </Link>
          </>
        }
      >
        {library.data ? (
          <div className="storage-location-card">
            <span className={`local-path-state ${library.data.settings.writable ? "is-ready" : "is-error"}`}>
              {library.data.settings.writable ? "可写入" : "不可写入"}
            </span>
            <code>{library.data.settings.host_path}</code>
            <button
              className="button button-secondary button-small"
              type="button"
              onClick={() => void navigator.clipboard
                .writeText(library.data!.settings.host_path)
                .then(() => setNotice("本地存储路径已复制"))
                .catch(() => setNotice("路径复制失败，请手动选择复制"))}
            >
              复制路径
            </button>
            {library.data.settings.restart_required ? <p>新位置已保存，重启本地服务后生效。</p> : null}
          </div>
        ) : null}
      </SideDrawer>
    </div>
  );
}
