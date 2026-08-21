import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode
} from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Link,
  NavLink,
  Navigate,
  Route,
  Routes,
  useLocation
} from "react-router-dom";
import { api } from "./api";
import { SideDrawer } from "./components";
import { Icon, type IconName } from "./icons";
import CookieProfilesPage from "./pages/CookieProfilesPage";
import DashboardPage from "./pages/DashboardPage";
import FileLibraryPage from "./pages/FileLibraryPage";
import NewTaskPage from "./pages/NewTaskPage";
import PublishingPage from "./pages/PublishingPage";
import PublishingQueuePage from "./pages/PublishingQueuePage";
import PublishingSettingsPage from "./pages/PublishingSettingsPage";
import ReviewDetailPage from "./pages/ReviewDetailPage";
import ReviewQueuePage from "./pages/ReviewQueuePage";
import SettingsPage from "./pages/SettingsPage";
import TaskDetailPage from "./pages/TaskDetailPage";
import TasksPage from "./pages/TasksPage";
import YouTubeMonitorFormPage from "./pages/YouTubeMonitorFormPage";
import YouTubeMonitorHistoryPage from "./pages/YouTubeMonitorHistoryPage";
import YouTubeMonitorsPage from "./pages/YouTubeMonitorsPage";
import { ThemeControl } from "./product-ui";

type NavigationItem = {
  to: string;
  label: string;
  description: string;
  icon: IconName;
  end?: boolean;
  group: "workspace" | "system";
};

const navigation: NavigationItem[] = [
  {
    to: "/",
    label: "工作台",
    description: "总览与优先队列",
    icon: "dashboard",
    end: true,
    group: "workspace"
  },
  {
    to: "/tasks",
    label: "任务",
    description: "进度与失败恢复",
    icon: "tasks",
    group: "workspace"
  },
  {
    to: "/reviews",
    label: "审核",
    description: "媒体与字幕复核",
    icon: "review",
    group: "workspace"
  },
  {
    to: "/files",
    label: "文件",
    description: "下载与处理结果",
    icon: "folder",
    group: "workspace"
  },
  {
    to: "/publishing",
    label: "投稿",
    description: "平台发布与失败恢复",
    icon: "route",
    group: "workspace"
  },
  {
    to: "/monitors",
    label: "监控",
    description: "YouTube 发现",
    icon: "monitor",
    group: "workspace"
  },
  {
    to: "/settings",
    label: "设置",
    description: "模型与处理策略",
    icon: "settings",
    group: "system"
  },
  {
    to: "/cookies",
    label: "Cookie",
    description: "登录凭据",
    icon: "cookie",
    group: "system"
  }
];

const commands = [
  {
    to: "/tasks/new",
    label: "新建任务",
    description: "从视频链接创建处理任务",
    icon: "plus" as IconName,
    shortcut: "Ctrl N",
    section: "最近"
  },
  {
    to: "/reviews",
    label: "跳转到审核",
    description: "打开等待处理的审核队列",
    icon: "review" as IconName,
    shortcut: "G R",
    section: "最近"
  },
  {
    to: "/cookies",
    label: "管理 Cookie",
    description: "查看登录凭据与同步状态",
    icon: "cookie" as IconName,
    section: "操作"
  },
  {
    to: "/monitors/new",
    label: "新建 YouTube 监控",
    description: "创建关键词、频道或完整剧集监控",
    icon: "monitor" as IconName,
    section: "操作"
  },
  {
    to: "/files",
    label: "重新扫描文件库",
    description: "打开文件库并检查本地文件状态",
    icon: "folder" as IconName,
    section: "操作"
  }
];

function NavGlyph({ name }: { name: IconName }) {
  return (
    <span className="nav-glyph" aria-hidden="true">
      <Icon name={name} />
    </span>
  );
}

function CommandPalette({
  open,
  onClose
}: {
  open: boolean;
  onClose: () => void;
}) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const [query, setQuery] = useState("");

  const results = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    if (!normalized) return commands;
    return commands.filter((item) =>
      `${item.label} ${item.description}`.toLocaleLowerCase().includes(normalized)
    );
  }, [query]);

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    if (open && !dialog.open) {
      setQuery("");
      dialog.showModal();
      window.setTimeout(() => inputRef.current?.focus(), 0);
    } else if (!open && dialog.open) {
      dialog.close();
    }
  }, [open]);

  return (
    <dialog
      ref={dialogRef}
      className="command-palette"
      aria-labelledby="command-palette-title"
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      onClose={onClose}
    >
      <div className="command-palette-search">
        <Icon name="search" />
        <label className="sr-only" htmlFor="command-search">
          搜索页面与命令
        </label>
        <input
          ref={inputRef}
          id="command-search"
          type="search"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="输入命令或搜索…"
        />
        <button className="command-palette-escape" type="button" aria-label="关闭命令面板" onClick={onClose}>
          ESC
        </button>
      </div>
      <div className="command-palette-body">
        <p id="command-palette-title">{results[0]?.section ?? "命令"}</p>
        {results.length === 0 ? (
          <div className="command-empty">没有匹配命令</div>
        ) : (
          <nav aria-label="命令结果">
            {results.map((item, index) => (
              <Link className={index === 0 ? "is-selected" : ""} to={item.to} onClick={onClose} key={item.to}>
                <NavGlyph name={item.icon} />
                <span>
                  <strong>{item.label}</strong>
                  <small>{item.description}</small>
                </span>
                {item.shortcut ? <kbd>{item.shortcut}</kbd> : null}
              </Link>
            ))}
          </nav>
        )}
      </div>
    </dialog>
  );
}

function Shell() {
  const [menuOpen, setMenuOpen] = useState(false);
  const [commandOpen, setCommandOpen] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const location = useLocation();
  const system = useQuery({
    queryKey: ["system-status"],
    queryFn: api.systemStatus,
    refetchInterval: 10_000
  });
  const drawerSummary = useQuery({
    queryKey: ["dashboard"],
    queryFn: api.dashboard,
    enabled: drawerOpen
  });

  useEffect(() => {
    setMenuOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    const handleShortcut = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setCommandOpen(true);
      }
    };
    window.addEventListener("keydown", handleShortcut);
    return () => window.removeEventListener("keydown", handleShortcut);
  }, []);

  const renderNavigation = (group: NavigationItem["group"], label: string) => (
    <div className="nav-group">
      <p>{label}</p>
      <nav className="primary-nav" aria-label={label}>
        {navigation
          .filter((item) => item.group === group)
          .map((item) => (
            <NavLink
              to={item.to}
              end={item.end}
              onClick={() => setMenuOpen(false)}
              key={item.to}
            >
              <NavGlyph name={item.icon} />
              <span>
                <strong>{item.label}</strong>
                <small>{item.description}</small>
              </span>
            </NavLink>
          ))}
      </nav>
    </div>
  );

  const controlReady = system.data?.database === "ready";

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">
        跳到主要内容
      </a>

      <aside className={`console-nav ${menuOpen ? "console-nav-open" : ""}`}>
        <div className="brand-lockup">
          <img src="/visoraft-mark.svg" alt="" />
          <div>
            <strong>Visoraft</strong>
            <span>本地媒体操作台</span>
          </div>
          <button
            className="nav-close"
            type="button"
            aria-label="关闭导航"
            onClick={() => setMenuOpen(false)}
          >
            <Icon name="close" />
          </button>
        </div>

        {renderNavigation("workspace", "工作空间")}
        {renderNavigation("system", "系统")}

        <div className="console-nav-footer">
          <p>本地运行状态</p>
          <span className={`live-line ${controlReady ? "" : "live-line-wait"}`}>
            <i aria-hidden="true" />
            {system.isPending
              ? "正在连接服务"
              : controlReady
                ? "各项服务运行正常"
                : "服务需要检查"}
          </span>
        </div>
      </aside>

      {menuOpen && (
        <button
          className="menu-backdrop"
          type="button"
          aria-label="关闭导航"
          onClick={() => setMenuOpen(false)}
        />
      )}

      <div className="content-shell">
        <header className="command-bar">
          <button
            className="menu-button"
            type="button"
            aria-label="打开导航"
            aria-expanded={menuOpen}
            onClick={() => setMenuOpen(true)}
          >
            <Icon name="menu" />
          </button>

          <button
            className="command-trigger"
            type="button"
            aria-label="搜索页面与命令"
            onClick={() => setCommandOpen(true)}
          >
            <Icon name="search" />
            <span>搜索页面与命令</span>
            <kbd>Ctrl K</kbd>
          </button>

          <div className="command-spacer" />
          <span
            className={`global-health ${controlReady ? "global-health-ready" : ""}`}
            role="status"
            aria-live="polite"
          >
            <i aria-hidden="true" />
            {controlReady ? "服务运行正常" : system.isPending ? "正在连接" : "服务异常"}
          </span>
          <ThemeControl />
          <button className="topbar-icon-button" type="button" aria-label="查看通知与运行概览" onClick={() => setDrawerOpen(true)}>
            <Icon name="bell" />
          </button>
          <NavLink to="/tasks/new" className="quick-create">
            <Icon name="plus" />
            <span className="quick-create-label">新建任务</span>
          </NavLink>
        </header>

        <main id="main-content" className="main-content">
          <Routes>
            <Route path="/" element={<DashboardPage />} />
            <Route path="/tasks" element={<TasksPage />} />
            <Route path="/tasks/new" element={<NewTaskPage />} />
            <Route path="/tasks/:taskId" element={<TaskDetailPage />} />
            <Route path="/reviews" element={<ReviewQueuePage />} />
            <Route path="/files" element={<FileLibraryPage />} />
            <Route path="/reviews/:taskId" element={<ReviewDetailPage />} />
            <Route path="/publishing" element={<PublishingQueuePage />} />
            <Route
              path="/publishing/settings"
              element={<PublishingSettingsPage />}
            />
            <Route path="/publishing/:taskId" element={<PublishingPage />} />
            <Route path="/monitors" element={<YouTubeMonitorsPage />} />
            <Route path="/monitors/new" element={<YouTubeMonitorFormPage />} />
            <Route
              path="/monitors/:monitorId/edit"
              element={<YouTubeMonitorFormPage />}
            />
            <Route
              path="/monitors/:monitorId/history"
              element={<YouTubeMonitorHistoryPage />}
            />
            <Route path="/settings" element={<SettingsPage />} />
            <Route path="/cookies" element={<CookieProfilesPage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </main>
      </div>

      <CommandPalette open={commandOpen} onClose={() => setCommandOpen(false)} />
      <SideDrawer
        open={drawerOpen}
        title="运行概览"
        onClose={() => setDrawerOpen(false)}
        footer={
          <>
            <button className="button button-secondary button-small" type="button" onClick={() => setDrawerOpen(false)}>
              关闭
            </button>
            <Link className="button button-primary button-small" to="/tasks" onClick={() => setDrawerOpen(false)}>
              查看任务
            </Link>
          </>
        }
      >
        {drawerSummary.isPending ? (
          <div className="drawer-loading" aria-busy="true">正在读取运行数据</div>
        ) : drawerSummary.isError ? (
          <div className="drawer-error" role="alert">运行数据暂时不可用</div>
        ) : (
          <div className="drawer-metric-list">
            <Link to="/tasks" onClick={() => setDrawerOpen(false)}>
              <span>处理中</span><strong>{drawerSummary.data?.active ?? 0}</strong>
            </Link>
            <Link to="/reviews" onClick={() => setDrawerOpen(false)}>
              <span>等待审核</span><strong>{drawerSummary.data?.awaiting_manual_review ?? 0}</strong>
            </Link>
            <Link to="/tasks?status=failed" onClick={() => setDrawerOpen(false)}>
              <span>需要处理</span><strong>{drawerSummary.data?.failed ?? 0}</strong>
            </Link>
            <Link to="/publishing" onClick={() => setDrawerOpen(false)}>
              <span>已投稿</span><strong>{drawerSummary.data?.published ?? 0}</strong>
            </Link>
          </div>
        )}
      </SideDrawer>
    </div>
  );
}

export default function App() {
  return <Shell />;
}
