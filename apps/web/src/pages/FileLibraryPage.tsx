import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, type MediaAsset } from "../api";
import { EmptyState, LoadingBlock, PageHeader, QueryError } from "../components";
import { assetKindLabel, formatBytes, formatDateTime, shortID, statusLabel } from "../format";
import { Icon } from "../icons";

function fileExtension(asset: MediaAsset) {
  const name = asset.original_name || asset.object_key;
  const dot = name.lastIndexOf(".");
  return dot >= 0 ? name.slice(dot + 1).toUpperCase() : "文件";
}

function isViewable(asset: MediaAsset) {
  return asset.content_type.startsWith("video/") ||
    asset.content_type.startsWith("audio/") ||
    asset.content_type.startsWith("image/") ||
    asset.content_type.startsWith("text/") ||
    asset.content_type === "application/json";
}

export default function FileLibraryPage() {
  const [query, setQuery] = useState("");
  const [showDeleted, setShowDeleted] = useState(false);
  const library = useQuery({
    queryKey: ["files"],
    queryFn: api.files,
    refetchInterval: 10_000
  });

  const folders = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    return (library.data?.folders ?? [])
      .map((folder) => ({
        ...folder,
        files: folder.files.filter((file) => {
          if (!showDeleted && (file.deleted_at || file.status === "deleted")) return false;
          if (!normalized) return true;
          return `${folder.title} ${file.original_name} ${assetKindLabel(file.kind)}`
            .toLocaleLowerCase()
            .includes(normalized);
        })
      }))
      .filter((folder) => folder.files.length > 0);
  }, [library.data?.folders, query, showDeleted]);

  return (
    <>
      <PageHeader
        title="文件中心"
        description="按任务查看下载的视频、封面、字幕和处理结果。文件由系统统一保管，可随时查看或下载到电脑。"
        actions={
          <Link className="button button-primary" to="/tasks/new">
            新建任务
          </Link>
        }
      />

      {library.isPending ? (
        <LoadingBlock label="正在整理任务文件" />
      ) : library.isError ? (
        <QueryError
          title="文件中心暂时不可用"
          message={library.error.message}
          retry={() => void library.refetch()}
        />
      ) : !library.data || library.data.file_count === 0 ? (
        <EmptyState
          title="还没有任务文件"
          description="创建任务并完成下载后，视频、封面和后续生成文件会出现在这里。"
          action={<Link className="button button-primary" to="/tasks/new">创建任务</Link>}
        />
      ) : (
        <>
          <section className="file-summary" aria-label="文件汇总">
            <div><span>任务文件夹</span><strong>{library.data.folder_count}</strong></div>
            <div><span>可用文件</span><strong>{library.data.available_count}</strong></div>
            <div><span>占用空间</span><strong>{formatBytes(library.data.total_bytes)}</strong></div>
            <div><span>已清理记录</span><strong>{library.data.deleted_count}</strong></div>
          </section>

          <section className="file-toolbar" aria-label="文件筛选">
            <label>
              <span className="sr-only">搜索文件</span>
              <Icon name="search" />
              <input
                type="search"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="搜索任务名称或文件名"
              />
            </label>
            <label className="file-deleted-toggle">
              <input
                type="checkbox"
                checked={showDeleted}
                onChange={(event) => setShowDeleted(event.target.checked)}
              />
              显示已清理记录
            </label>
          </section>

          {folders.length === 0 ? (
            <EmptyState title="没有匹配文件" description="调整搜索条件或显示已清理记录后再试。" />
          ) : (
            <div className="file-folders">
              {folders.map((folder) => (
                <details className="file-folder" open key={folder.task_id}>
                  <summary>
                    <span className="file-folder-icon"><Icon name="folder" /></span>
                    <div>
                      <strong title={folder.title}>{folder.title}</strong>
                      <small>
                        任务 #{shortID(folder.task_id)} · {folder.available_count} 个可用文件 · {formatBytes(folder.total_bytes)}
                      </small>
                    </div>
                    <span className="file-folder-state">
                      {folder.archived ? "已归档" : statusLabel(folder.status)}
                    </span>
                    <Link to={`/tasks/${folder.task_id}`} onClick={(event) => event.stopPropagation()}>
                      查看任务
                    </Link>
                  </summary>
                  <div className="file-list" role="list">
                    {folder.files.map((file) => {
                      const deleted = Boolean(file.deleted_at) || file.status === "deleted";
                      const contentURL = api.assetContentURL(folder.task_id, file.id);
                      return (
                        <article className={`file-row ${deleted ? "file-row-deleted" : ""}`} role="listitem" key={file.id}>
                          <span className="file-type-icon"><Icon name="file" /></span>
                          <div className="file-name">
                            <strong title={file.original_name}>{file.original_name || assetKindLabel(file.kind)}</strong>
                            <small>{assetKindLabel(file.kind)} · {fileExtension(file)}</small>
                          </div>
                          <div className="file-location">
                            <span>文件位置</span>
                            <strong>任务文件 / {shortID(folder.task_id)} / {file.original_name}</strong>
                          </div>
                          <div className="file-meta">
                            <span>{deleted ? "已清理" : formatBytes(file.size_bytes)}</span>
                            <small>{formatDateTime(file.created_at)}</small>
                          </div>
                          <div className="file-actions">
                            {!deleted && isViewable(file) && (
                              <a className="button button-secondary button-small" href={contentURL} target="_blank" rel="noreferrer">
                                查看
                              </a>
                            )}
                            {!deleted && (
                              <a className="button button-primary button-small" href={contentURL} download={file.original_name}>
                                下载
                              </a>
                            )}
                          </div>
                        </article>
                      );
                    })}
                  </div>
                </details>
              ))}
            </div>
          )}
        </>
      )}
    </>
  );
}
