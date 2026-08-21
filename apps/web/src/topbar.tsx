import { useEffect, useState, type ChangeEventHandler, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Icon } from "./icons";

export function TopBarActions({ children }: { children: ReactNode }) {
  const [target, setTarget] = useState<HTMLElement | null>(null);

  useEffect(() => {
    setTarget(document.getElementById("page-topbar-actions"));
    return () => setTarget(null);
  }, []);

  return target ? createPortal(children, target) : null;
}

export function TopBarSearch({
  value,
  placeholder,
  shortcut = "Ctrl F",
  onChange
}: {
  value: string;
  placeholder: string;
  shortcut?: string;
  onChange: ChangeEventHandler<HTMLInputElement>;
}) {
  const [target, setTarget] = useState<HTMLElement | null>(null);

  useEffect(() => {
    const root = document.documentElement;
    root.classList.add("has-page-topbar-search");
    setTarget(document.getElementById("page-topbar-search"));
    return () => {
      root.classList.remove("has-page-topbar-search");
      setTarget(null);
    };
  }, []);

  return target
    ? createPortal(
        <label className="topbar-page-search">
          <span className="sr-only">{placeholder}</span>
          <Icon name="search" />
          <input type="search" value={value} onChange={onChange} placeholder={placeholder} />
          <kbd>{shortcut}</kbd>
        </label>,
        target
      )
    : null;
}
