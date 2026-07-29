import { afterEach, expect, test } from "bun:test";
import { dashPathFromPathname, navigate, setNavigationBlocker, tabFromPathname } from "../src/router";

const originalWindow = globalThis.window;
afterEach(() => {
  setNavigationBlocker(null);
  Object.defineProperty(globalThis, "window", { configurable: true, value: originalWindow });
});

test("dashboard routes decode safely", () => {
  expect(tabFromPathname("/dash/folder/report")).toBe("dash");
  expect(dashPathFromPathname("/dash/Folder/My%20Report")).toBe("folder/my report");
  expect(dashPathFromPathname("/dash/%zz")).toBe("");
});

test("navigation blocker protects the current location", () => {
  const location = { pathname: "/dash/first", search: "" };
  const apply = (to: string | URL | null | undefined) => {
    const url = new URL(String(to), "http://dux.test" + location.pathname + location.search);
    location.pathname = url.pathname;
    location.search = url.search;
  };
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: {
      location: { ...location, href: "http://dux.test/dash/first" },
      history: { pushState: (_: unknown, __: string, to?: string | URL | null) => apply(to) },
    },
  });

  setNavigationBlocker(() => false);
  expect(navigate("/dash/second")).toBeFalse();
  expect(location.pathname).toBe("/dash/first");
});
