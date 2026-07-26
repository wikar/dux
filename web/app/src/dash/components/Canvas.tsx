import { useLayoutEffect, useMemo, useRef, useState } from "react";
import styles from "./Canvas.module.css";
import { imageUrl } from "../api";
import { useResolvedTheme } from "../data";
import { useDocStore, useUiStore } from "../store";
import type { BackgroundFit } from "../types";
import ContextMenu, { type MenuState } from "./ContextMenu";
import ElementView from "./ElementView";

const FIT_CSS: Record<BackgroundFit, React.CSSProperties> = {
  cover: { backgroundSize: "cover", backgroundRepeat: "no-repeat" },
  contain: { backgroundSize: "contain", backgroundRepeat: "no-repeat", backgroundPosition: "center" },
  fill: { backgroundSize: "100% 100%", backgroundRepeat: "no-repeat" },
  tile: { backgroundSize: "auto", backgroundRepeat: "repeat" },
};

/** Is a CSS color light? Parsed by the browser rather than by a hand-rolled
 *  reader, so every notation the theme accepts works. Only feeds color-scheme,
 *  so compositing a translucent color over black is close enough. */
function isLight(css: string): boolean {
  const ctx = document.createElement("canvas").getContext("2d");
  if (!ctx) return false;
  ctx.fillStyle = "#000000"; // an unparseable color leaves the previous value
  ctx.fillStyle = css;
  ctx.fillRect(0, 0, 1, 1);
  const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data;
  return (r * 299 + g * 587 + b * 114) / 1000 > 128;
}

export default function Canvas() {
  const doc = useDocStore((s) => s.doc)!;
  const mode = useUiStore((s) => s.mode);
  const fullscreen = useUiStore((s) => s.fullscreen);
  const select = useUiStore((s) => s.select);
  const clearCrossFilters = useUiStore((s) => s.clearCrossFilters);
  const outerRef = useRef<HTMLDivElement>(null);
  const [scale, setScale] = useState(1);
  const [menu, setMenu] = useState<MenuState | null>(null);

  const { width, height } = doc.canvas;

  // Scale-to-fit: the fixed-size canvas is scaled to the available area.
  useLayoutEffect(() => {
    const el = outerRef.current;
    if (!el) return;
    const pad = fullscreen ? 0 : 24;
    const fit = () => {
      const w = el.clientWidth - pad * 2;
      const h = el.clientHeight - pad * 2;
      setScale(Math.max(0.05, Math.min(w / width, h / height)));
    };
    fit();
    const ro = new ResizeObserver(fit);
    ro.observe(el);
    return () => ro.disconnect();
  }, [width, height, fullscreen]);

  // Theme cascade: the dashboard's own canvas.background wins over theme
  // tokens; element chrome tokens flow down as CSS variables.
  const theme = useResolvedTheme(doc);
  const bg = doc.canvas.background;
  const canvasBg = bg?.color || theme.background;

  // The base tokens every derived one in index.css is built from. --th-accent
  // is the first palette entry: an author already picks it, and links, active
  // pills and cross-filter highlights all want the dashboard's own accent.
  const vars = useMemo(
    () => ({
      "--th-bg": canvasBg,
      "--th-el-bg": theme.elementBackground,
      "--th-title-bg": theme.titleBackground,
      "--th-border": theme.border,
      "--th-text": theme.text,
      "--th-accent": theme.palette[0] ?? theme.text,
      // Native controls — date inputs, scrollbars, focus rings — follow the
      // theme's own lightness instead of the dark app shell's.
      "color-scheme": isLight(canvasBg) ? "light" : "dark",
    }),
    [canvasBg, theme]
  );

  // Set on <html>, not the canvas: the slicer dropdown and the filter funnel
  // portal to <body> and would otherwise fall back to the dark app palette.
  useLayoutEffect(() => {
    const root = document.documentElement.style;
    for (const [k, v] of Object.entries(vars)) root.setProperty(k, v);
    return () => {
      for (const k of Object.keys(vars)) root.removeProperty(k);
    };
  }, [vars]);

  const canvasStyle: React.CSSProperties = {
    width,
    height,
    transform: `scale(${scale})`,
    transformOrigin: "0 0",
    background: canvasBg,
    color: theme.text,
    fontFamily: theme.fontFamily,
  };
  const bgImage = bg?.url ?? bg?.asset ?? theme.backgroundImage;
  if (bgImage) {
    Object.assign(canvasStyle, FIT_CSS[bg?.fit ?? theme.backgroundFit]);
    canvasStyle.backgroundImage = `url(${imageUrl(bgImage)})`;
    canvasStyle.backgroundColor = canvasBg;
  }

  // Stable z-sort: by z, ties by document order.
  const sorted = doc.elements
    .map((el, i) => ({ el, i }))
    .sort((a, b) => (a.el.layout.z ?? 0) - (b.el.layout.z ?? 0) || a.i - b.i);

  return (
    <div
      ref={outerRef}
      className={`${styles.outer} ${fullscreen ? styles.fullscreen : ""}`}
      onPointerDown={(e) => {
        select(null);
        setMenu(null);
        // A click outside any element clears chart cross-filter selections;
        // clicks landing inside a visual (marks, chrome) are left alone.
        if (!(e.target as HTMLElement).closest("[data-element-id]")) clearCrossFilters();
      }}
    >
      <div className={styles.sizer} style={{ width: width * scale, height: height * scale }}>
        <div className={styles.canvas} style={canvasStyle}>
          {mode === "edit" && <div className={styles.grid} />}
          {sorted.map(({ el }) => (
            <ElementView
              key={el.id}
              el={el}
              canvas={doc.canvas}
              scale={scale}
              onContextMenu={(x, y) => setMenu({ x, y, id: el.id })}
            />
          ))}
        </div>
      </div>
      {menu && mode === "edit" && <ContextMenu menu={menu} onClose={() => setMenu(null)} />}
    </div>
  );
}
