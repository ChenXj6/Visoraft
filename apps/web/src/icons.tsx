import type { ReactNode, SVGProps } from "react";

export type IconName =
  | "dashboard"
  | "tasks"
  | "review"
  | "monitor"
  | "settings"
  | "cookie"
  | "search"
  | "menu"
  | "close"
  | "alert"
  | "activity"
  | "errorCircle"
  | "checkCircle"
  | "plus"
  | "timeRange"
  | "media"
  | "play"
  | "chevronDown"
  | "route"
  | "shield"
  | "sliders"
  | "subtitles"
  | "model"
  | "prompt"
  | "history"
  | "discovery"
  | "channel"
  | "refresh"
  | "folder"
  | "file"
  | "palette"
  | "sun"
  | "moon"
  | "bell"
  | "help"
  | "external"
  | "layers"
  | "bolt"
  | "screen"
  | "message";

const paths: Record<IconName, ReactNode> = {
  dashboard: (
    <>
      <path d="M4 5.5A1.5 1.5 0 0 1 5.5 4h4A1.5 1.5 0 0 1 11 5.5v4A1.5 1.5 0 0 1 9.5 11h-4A1.5 1.5 0 0 1 4 9.5z" />
      <path d="M13 5.5A1.5 1.5 0 0 1 14.5 4h4A1.5 1.5 0 0 1 20 5.5v4a1.5 1.5 0 0 1-1.5 1.5h-4A1.5 1.5 0 0 1 13 9.5z" />
      <path d="M4 14.5A1.5 1.5 0 0 1 5.5 13h4a1.5 1.5 0 0 1 1.5 1.5v4A1.5 1.5 0 0 1 9.5 20h-4A1.5 1.5 0 0 1 4 18.5z" />
      <path d="M13 14.5a1.5 1.5 0 0 1 1.5-1.5h4a1.5 1.5 0 0 1 1.5 1.5v4a1.5 1.5 0 0 1-1.5 1.5h-4a1.5 1.5 0 0 1-1.5-1.5z" />
    </>
  ),
  tasks: (
    <>
      <path d="M8 6h12M8 12h12M8 18h12" />
      <path d="M4 6h.01M4 12h.01M4 18h.01" />
    </>
  ),
  review: (
    <>
      <path d="M6.5 4h11A1.5 1.5 0 0 1 19 5.5v13a1.5 1.5 0 0 1-1.5 1.5h-11A1.5 1.5 0 0 1 5 18.5v-13A1.5 1.5 0 0 1 6.5 4Z" />
      <path d="m8 12 2.4 2.4L16 8.8" />
    </>
  ),
  monitor: (
    <>
      <path d="M4 15h3l2-6 3.5 10L15 12l1.5 3H20" />
      <path d="M5.5 4h13A1.5 1.5 0 0 1 20 5.5v13a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 18.5v-13A1.5 1.5 0 0 1 5.5 4Z" />
    </>
  ),
  settings: (
    <>
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.7 1.7 0 0 0 .34 1.88l.06.06-2.83 2.83-.06-.06a1.7 1.7 0 0 0-1.88-.34 1.7 1.7 0 0 0-1.03 1.56V21h-4v-.08A1.7 1.7 0 0 0 8.96 19.4a1.7 1.7 0 0 0-1.88.34l-.06.06-2.83-2.83.06-.06A1.7 1.7 0 0 0 4.6 15a1.7 1.7 0 0 0-1.56-1.03H3v-4h.08A1.7 1.7 0 0 0 4.6 8.96a1.7 1.7 0 0 0-.34-1.88l-.06-.06 2.83-2.83.06.06a1.7 1.7 0 0 0 1.88.34H9A1.7 1.7 0 0 0 10 3.08V3h4v.08a1.7 1.7 0 0 0 1.03 1.56 1.7 1.7 0 0 0 1.88-.34l.06-.06 2.83 2.83-.06.06a1.7 1.7 0 0 0-.34 1.88v-.04A1.7 1.7 0 0 0 20.92 10H21v4h-.08A1.7 1.7 0 0 0 19.4 15Z" />
    </>
  ),
  cookie: (
    <>
      <path d="M20 12.3A8 8 0 1 1 11.7 4a4.2 4.2 0 0 0 5.8 5.8A4.2 4.2 0 0 0 20 12.3Z" />
      <path d="M8.5 9.5h.01M9.5 15h.01M14.5 14h.01" />
    </>
  ),
  search: (
    <>
      <circle cx="11" cy="11" r="6.5" />
      <path d="m16 16 4 4" />
    </>
  ),
  menu: <path d="M4 7h16M4 12h16M4 17h16" />,
  close: <path d="m6 6 12 12M18 6 6 18" />,
  alert: (
    <>
      <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0Z" />
      <path d="M12 9v4M12 17h.01" />
    </>
  ),
  activity: (
    <>
      <path d="M3 12h4l2.2-7 4.1 14 2.2-7H21" />
    </>
  ),
  errorCircle: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v6M12 17h.01" />
    </>
  ),
  checkCircle: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="m8 12 2.5 2.5L16.5 8" />
    </>
  ),
  plus: <path d="M12 5v14M5 12h14" />,
  timeRange: (
    <>
      <circle cx="6.5" cy="12" r="2.5" />
      <circle cx="17.5" cy="12" r="2.5" />
      <path d="M9 12h6M6.5 7V5M17.5 7V5" />
    </>
  ),
  media: (
    <>
      <rect x="3.5" y="5" width="17" height="14" rx="2" />
      <path d="m10 9 5 3-5 3Z" />
    </>
  ),
  play: <path d="m8 5 11 7-11 7Z" />,
  chevronDown: <path d="m7 10 5 5 5-5" />,
  route: (
    <>
      <circle cx="6" cy="6" r="2" />
      <circle cx="18" cy="8" r="2" />
      <circle cx="18" cy="18" r="2" />
      <path d="M8 6h2a4 4 0 0 1 4 4v4a4 4 0 0 0 4 4M14 11a3 3 0 0 1 3-3" />
    </>
  ),
  shield: (
    <>
      <path d="M12 3.5 19 6v5.2c0 4.3-2.8 7.6-7 9.3-4.2-1.7-7-5-7-9.3V6Z" />
      <circle cx="12" cy="11" r="2" />
      <path d="M12 13v3" />
    </>
  ),
  sliders: (
    <>
      <path d="M4 6h6M14 6h6M4 12h10M18 12h2M4 18h3M11 18h9" />
      <circle cx="12" cy="6" r="2" />
      <circle cx="16" cy="12" r="2" />
      <circle cx="9" cy="18" r="2" />
    </>
  ),
  subtitles: (
    <>
      <rect x="3.5" y="5" width="17" height="14" rx="2" />
      <path d="M7 11h4M13 11h4M7 15h7" />
    </>
  ),
  model: (
    <>
      <circle cx="12" cy="12" r="3" />
      <circle cx="5" cy="6" r="2" />
      <circle cx="19" cy="6" r="2" />
      <circle cx="12" cy="20" r="2" />
      <path d="m7 7.7 2.7 2.1M17 7.7l-2.7 2.1M12 15v3" />
    </>
  ),
  prompt: (
    <>
      <path d="M8 5H5v14h3M16 5h3v14h-3" />
      <path d="m10 9 4 3-4 3" />
    </>
  ),
  history: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7v5l3.5 2" />
      <path d="M7 4.8 4.8 7" />
    </>
  ),
  discovery: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <circle cx="12" cy="12" r="4.5" />
      <circle cx="16.5" cy="8" r="1.4" />
      <path d="M12 12 18 6" />
    </>
  ),
  channel: (
    <>
      <path d="M12 18V9" />
      <circle cx="12" cy="6" r="2" />
      <path d="M7.5 10.5a6.4 6.4 0 0 1 0-9M16.5 1.5a6.4 6.4 0 0 1 0 9M5 13a10 10 0 0 1 0-14M19-1a10 10 0 0 1 0 14" />
      <path d="M8 20h8" />
    </>
  ),
  refresh: (
    <>
      <path d="M18.2 9A6.8 6.8 0 0 0 7 6.8M5.8 15A6.8 6.8 0 0 0 17 17.2" />
      <circle cx="18.4" cy="7.5" r="1.4" />
      <circle cx="5.6" cy="16.5" r="1.4" />
    </>
  ),
  folder: (
    <>
      <path d="M3.5 7.5A1.5 1.5 0 0 1 5 6h5l2 2h7A1.5 1.5 0 0 1 20.5 9.5v8A1.5 1.5 0 0 1 19 19H5a1.5 1.5 0 0 1-1.5-1.5Z" />
      <path d="M3.5 10h17" />
    </>
  ),
  file: (
    <>
      <path d="M7 3.5h6l4 4v13H7a2 2 0 0 1-2-2v-13a2 2 0 0 1 2-2Z" />
      <path d="M13 3.5v4h4M8.5 12h5M8.5 16h7" />
    </>
  ),
  palette: (
    <>
      <path d="M12 3.5a8.5 8.5 0 1 0 0 17h1.2a1.8 1.8 0 0 0 1.2-3.15 1.8 1.8 0 0 1 1.2-3.15H17a3.5 3.5 0 0 0 3.5-3.5C20.5 6.7 16.7 3.5 12 3.5Z" />
      <circle cx="8" cy="9" r="1" /><circle cx="12" cy="7" r="1" /><circle cx="16" cy="9" r="1" /><circle cx="8" cy="14" r="1" />
    </>
  ),
  sun: (
    <>
      <circle cx="12" cy="12" r="3.5" />
      <path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M18.7 5.3l-1.4 1.4M6.7 17.3l-1.4 1.4" />
    </>
  ),
  moon: <path d="M19.5 15.2A8 8 0 0 1 8.8 4.5 8.5 8.5 0 1 0 19.5 15.2Z" />,
  bell: (
    <>
      <path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9" />
      <path d="M10 21h4" />
    </>
  ),
  help: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M9.8 9a2.4 2.4 0 1 1 3.6 2.1c-.9.5-1.4 1-1.4 2.1M12 17h.01" />
    </>
  ),
  external: (
    <>
      <path d="M14 5h5v5M19 5l-8 8" />
      <path d="M17 13v5a1.5 1.5 0 0 1-1.5 1.5h-10A1.5 1.5 0 0 1 4 18V8a1.5 1.5 0 0 1 1.5-1.5h5" />
    </>
  ),
  layers: (
    <>
      <path d="m12 2 10 5-10 5L2 7Z" />
      <path d="m2 12 10 5 10-5M2 17l10 5 10-5" />
    </>
  ),
  bolt: <path d="M13 2 3 14h9l-1 8 10-12h-9Z" />,
  screen: (
    <>
      <rect x="2.5" y="3.5" width="19" height="14" rx="2" />
      <path d="M8 21h8M12 17.5V21M2.5 7.5h19" />
    </>
  ),
  message: <path d="M21 15a4 4 0 0 1-4 4H8l-5 3V7a4 4 0 0 1 4-4h10a4 4 0 0 1 4 4Z" />
};

export function Icon({
  name,
  ...props
}: { name: IconName } & SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      {...props}
    >
      {paths[name]}
    </svg>
  );
}
