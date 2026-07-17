// Minimal history-API router shared by the app shell and the dash section.
// One route shape per tab: "/", "/explorer", "/dash/<dashboard path>".
import { useSyncExternalStore } from "react";

const listeners = new Set<() => void>();

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  window.addEventListener("popstate", cb);
  return () => {
    listeners.delete(cb);
    window.removeEventListener("popstate", cb);
  };
}

export function usePathname(): string {
  return useSyncExternalStore(subscribe, () => window.location.pathname);
}

export function navigate(to: string) {
  window.history.pushState({}, "", to);
  listeners.forEach((l) => l());
}

export type Tab = "home" | "explorer" | "dash";

export function tabFromPathname(pathname: string): Tab {
  if (pathname === "/explorer") return "explorer";
  if (pathname === "/dash" || pathname.startsWith("/dash/")) return "dash";
  return "home";
}

/** Dashboard identity from a /dash/... URL ("" when none is open). */
export function dashPathFromPathname(pathname: string): string {
  if (pathname !== "/dash" && !pathname.startsWith("/dash/")) return "";
  return decodeURIComponent(pathname.slice("/dash".length))
    .replace(/^\/+|\/+$/g, "")
    .toLowerCase();
}
