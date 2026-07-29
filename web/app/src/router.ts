// Minimal history-API router shared by the app shell and the dash section.
// One route shape per tab: "/", "/explorer", "/dash/<dashboard path>".
import { useSyncExternalStore } from "react";

const listeners = new Set<() => void>();
type NavigationBlocker = (to: string) => boolean;
let blocker: NavigationBlocker | null = null;
let acceptedLocation = "";

const locationKey = () => window.location.pathname + window.location.search;

function notify() {
  listeners.forEach((listener) => listener());
}

function onPopState() {
  const next = locationKey();
  if (blocker && !blocker(next)) {
    window.history.pushState({}, "", acceptedLocation);
    return;
  }
  acceptedLocation = next;
  notify();
}

function subscribe(cb: () => void): () => void {
  if (listeners.size === 0) {
    acceptedLocation = locationKey();
    window.addEventListener("popstate", onPopState);
  }
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
    if (listeners.size === 0) window.removeEventListener("popstate", onPopState);
  };
}

export function usePathname(): string {
  const location = useSyncExternalStore(subscribe, locationKey);
  return location.split("?", 1)[0];
}

export function useSearch(): string {
  const location = useSyncExternalStore(subscribe, locationKey);
  const query = location.indexOf("?");
  return query < 0 ? "" : location.slice(query);
}

export function setNavigationBlocker(next: NavigationBlocker | null) {
  blocker = next;
}

export function navigate(to: string): boolean {
  const target = new URL(to, window.location.href);
  const next = target.pathname + target.search;
  if (next !== locationKey() && blocker && !blocker(next)) return false;
  window.history.pushState({}, "", to);
  acceptedLocation = locationKey();
  notify();
  return true;
}

export function replaceLocation(to: string) {
  window.history.replaceState({}, "", to);
  acceptedLocation = locationKey();
  notify();
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
  try {
    return decodeURIComponent(pathname.slice("/dash".length))
      .replace(/^\/+|\/+$/g, "")
      .toLowerCase();
  } catch {
    return "";
  }
}
