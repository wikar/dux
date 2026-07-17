import { useLayoutEffect, useRef, useState } from "react";
import styles from "./Canvas.module.css";
import { assetUrl } from "../api";
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

export default function Canvas() {
  const doc = useDocStore((s) => s.doc)!;
  const mode = useUiStore((s) => s.mode);
  const select = useUiStore((s) => s.select);
  const outerRef = useRef<HTMLDivElement>(null);
  const [scale, setScale] = useState(1);
  const [menu, setMenu] = useState<MenuState | null>(null);

  const { width, height } = doc.canvas;

  // Scale-to-fit: the fixed-size canvas is scaled to the available area.
  useLayoutEffect(() => {
    const el = outerRef.current;
    if (!el) return;
    const PAD = 24;
    const fit = () => {
      const w = el.clientWidth - PAD * 2;
      const h = el.clientHeight - PAD * 2;
      setScale(Math.max(0.05, Math.min(w / width, h / height)));
    };
    fit();
    const ro = new ResizeObserver(fit);
    ro.observe(el);
    return () => ro.disconnect();
  }, [width, height]);

  const bg = doc.canvas.background;
  const canvasStyle: React.CSSProperties = {
    width,
    height,
    transform: `scale(${scale})`,
    transformOrigin: "0 0",
    background: bg?.color || "var(--base)",
  };
  if (bg?.asset) {
    Object.assign(canvasStyle, FIT_CSS[bg.fit ?? "cover"]);
    canvasStyle.backgroundImage = `url(${assetUrl(bg.asset)})`;
    if (bg.color) canvasStyle.backgroundColor = bg.color;
  }

  // Stable z-sort: by z, ties by document order.
  const sorted = doc.elements
    .map((el, i) => ({ el, i }))
    .sort((a, b) => (a.el.layout.z ?? 0) - (b.el.layout.z ?? 0) || a.i - b.i);

  return (
    <div
      ref={outerRef}
      className={styles.outer}
      onPointerDown={() => {
        select(null);
        setMenu(null);
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
