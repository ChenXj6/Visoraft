import {
  createContext,
  useContext,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type ReactNode
} from "react";
import { Icon } from "./icons";

type ThemeMode = "light" | "dark" | "system";
type Accent = "blue" | "teal" | "violet";

type ThemeContextValue = {
  mode: ThemeMode;
  accent: Accent;
  resolved: "light" | "dark";
  setMode: (mode: ThemeMode) => void;
  setAccent: (accent: Accent) => void;
};

const ThemeContext = createContext<ThemeContextValue | null>(null);

function storedThemeMode(): ThemeMode {
  const value = window.localStorage.getItem("visoraft-theme");
  return value === "light" || value === "dark" || value === "system"
    ? value
    : "system";
}

function storedAccent(): Accent {
  const value = window.localStorage.getItem("visoraft-accent");
  return value === "blue" || value === "teal" || value === "violet"
    ? value
    : "blue";
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [mode, setMode] = useState<ThemeMode>(storedThemeMode);
  const [accent, setAccent] = useState<Accent>(storedAccent);
  const [systemDark, setSystemDark] = useState(() =>
    window.matchMedia("(prefers-color-scheme: dark)").matches
  );
  const resolved = mode === "system" ? (systemDark ? "dark" : "light") : mode;

  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const update = () => setSystemDark(media.matches);
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);

  useEffect(() => {
    document.documentElement.dataset.theme = resolved;
    document.documentElement.dataset.accent = accent;
    document.documentElement.style.colorScheme = resolved;
    window.localStorage.setItem("visoraft-theme", mode);
    window.localStorage.setItem("visoraft-accent", accent);
  }, [accent, mode, resolved]);

  const value = useMemo(
    () => ({ mode, accent, resolved, setMode, setAccent }),
    [accent, mode, resolved]
  );
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

function useTheme() {
  const value = useContext(ThemeContext);
  if (!value) throw new Error("ThemeProvider is missing");
  return value;
}

const modeLabels: Record<ThemeMode, string> = {
  light: "浅色",
  dark: "深色",
  system: "跟随系统"
};

const accentLabels: Record<Accent, string> = {
  blue: "海湾蓝",
  teal: "松石青",
  violet: "暮光紫"
};

export function ThemeControl() {
  const { mode, accent, resolved, setMode, setAccent } = useTheme();
  return (
    <details className="theme-control">
      <summary aria-label="外观设置" title="外观设置">
        <Icon name={resolved === "dark" ? "moon" : "sun"} />
        <span>外观</span>
      </summary>
      <div className="theme-popover">
        <header>
          <Icon name="palette" />
          <div>
            <strong>界面外观</strong>
            <small>偏好只保存在当前浏览器</small>
          </div>
        </header>
        <fieldset>
          <legend>明暗模式</legend>
          <div className="theme-options">
            {(Object.keys(modeLabels) as ThemeMode[]).map((item) => (
              <button
                type="button"
                className={mode === item ? "is-active" : ""}
                aria-pressed={mode === item}
                onClick={() => setMode(item)}
                key={item}
              >
                {modeLabels[item]}
              </button>
            ))}
          </div>
        </fieldset>
        <fieldset>
          <legend>强调色</legend>
          <div className="accent-options">
            {(Object.keys(accentLabels) as Accent[]).map((item) => (
              <button
                type="button"
                className={accent === item ? "is-active" : ""}
                aria-label={accentLabels[item]}
                aria-pressed={accent === item}
                onClick={() => setAccent(item)}
                key={item}
              >
                <i className={`accent-swatch accent-${item}`} aria-hidden="true" />
                {accentLabels[item]}
              </button>
            ))}
          </div>
        </fieldset>
      </div>
    </details>
  );
}

export function TransientNotice({
  children,
  tone = "success",
  onDismiss,
  duration = tone === "error" ? 8_000 : 4_500
}: {
  children: ReactNode;
  tone?: "success" | "error" | "info";
  onDismiss: () => void;
  duration?: number;
}) {
  const [paused, setPaused] = useState(false);
  useEffect(() => {
    if (paused) return;
    const timeout = window.setTimeout(onDismiss, duration);
    return () => window.clearTimeout(timeout);
  }, [duration, onDismiss, paused]);

  return (
    <div
      className={`transient-notice transient-notice-${tone}`}
      role={tone === "error" ? "alert" : "status"}
      aria-live={tone === "error" ? "assertive" : "polite"}
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
    >
      <span className="notice-symbol" aria-hidden="true">
        {tone === "error" ? "!" : "✓"}
      </span>
      <div>{children}</div>
      <button type="button" aria-label="关闭提示" onClick={onDismiss}>
        <Icon name="close" />
      </button>
      <span className="notice-timer" style={{ animationDuration: `${duration}ms` }} />
    </div>
  );
}

export function HelpLink({
  label = "查看说明",
  title,
  children
}: {
  label?: string;
  title: string;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const titleId = useId();
  const dialogRef = useRef<HTMLDialogElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    if (open && !dialog.open) {
      dialog.showModal();
      closeRef.current?.focus();
    } else if (!open && dialog.open) {
      dialog.close();
    }
  }, [open]);

  return (
    <>
      <button className="help-link" type="button" onClick={() => setOpen(true)}>
        <Icon name="help" />
        {label}
      </button>
      <dialog
        ref={dialogRef}
        className="help-dialog"
        aria-labelledby={titleId}
        onCancel={(event) => {
          event.preventDefault();
          setOpen(false);
        }}
        onClose={() => setOpen(false)}
      >
        <header>
          <div>
            <span className="help-dialog-icon"><Icon name="help" /></span>
            <h2 id={titleId}>{title}</h2>
          </div>
          <button
            ref={closeRef}
            type="button"
            aria-label="关闭说明"
            onClick={() => setOpen(false)}
          >
            <Icon name="close" />
          </button>
        </header>
        <div className="help-dialog-body">{children}</div>
        <footer>
          <button className="button button-primary" type="button" onClick={() => setOpen(false)}>
            我知道了
          </button>
        </footer>
      </dialog>
    </>
  );
}

const languageOptions = [
  ["auto", "自动识别"],
  ["zh", "中文"],
  ["zh-CN", "简体中文"],
  ["zh-TW", "繁体中文"],
  ["en", "英语"],
  ["ja", "日语"],
  ["ko", "韩语"],
  ["es", "西班牙语"],
  ["fr", "法语"],
  ["de", "德语"],
  ["pt", "葡萄牙语"],
  ["ru", "俄语"],
  ["ar", "阿拉伯语"],
  ["vi", "越南语"],
  ["th", "泰语"],
  ["id", "印尼语"]
] as const;

export function LanguageSelect({
  value,
  onChange,
  allowAuto = true,
  id
}: {
  value: string;
  onChange: (value: string) => void;
  allowAuto?: boolean;
  id?: string;
}) {
  const known = languageOptions.some(([code]) => code === value);
  return (
    <select id={id} value={known ? value : "other"} onChange={(event) => onChange(event.target.value)}>
      {languageOptions
        .filter(([code]) => allowAuto || code !== "auto")
        .map(([code, label]) => (
          <option value={code} key={code}>{label}</option>
        ))}
      {!known && <option value="other">其他语言（{value}）</option>}
    </select>
  );
}

export function ExternalGuideLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a className="external-guide-link" href={href} target="_blank" rel="noreferrer">
      {children}
      <Icon name="external" />
    </a>
  );
}
