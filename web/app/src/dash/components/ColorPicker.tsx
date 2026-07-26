// A swatch button that opens the Sketch panel (hex field + alpha slider +
// RGBA boxes) in a portal.
//
// Replaces <input type="color">: that dialog is the browser's own — it can't
// be themed, and it carries no alpha channel outside Chromium 133+.
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import Sketch from "@uiw/react-color-sketch";
import styles from "./ColorPicker.module.css";
import { normalizeColor, toHexa } from "./color";
import { useDocStore } from "../store";

// Sketch's own default width, and the height it renders at with the preset
// swatches off. Only used to decide where the panel fits.
const PANEL_W = 218;
const PANEL_H = 230;
const GAP = 6;

const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(v, hi));

export default function ColorPicker({
  value,
  onChange,
  className,
  title,
}: {
  /** Current color, any CSS form parseColor understands. */
  value: string;
  /** Called live while the panel is open, and once more on close. */
  onChange: (v: string) => void;
  className: string;
  title?: string;
}) {
  const btnRef = useRef<HTMLButtonElement>(null);
  const popRef = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null);
  // The value the panel opened on, and the latest one it emitted — both are
  // needed to collapse the drag into a single undo step on close.
  const opened = useRef(value);
  const latest = useRef(value);

  const open = pos !== null;

  const close = () => {
    setPos(null);
    // A drag emits a change per pointermove; recording each would blow the
    // 100-step undo history away. Rewind to where the panel opened (still
    // untracked), resume, then re-apply — one entry, original → final.
    const t = useDocStore.temporal.getState();
    if (latest.current !== opened.current) {
      onChange(opened.current);
      t.resume();
      onChange(latest.current);
    } else {
      t.resume();
    }
  };

  const toggle = () => {
    if (open) {
      close();
      return;
    }
    const r = btnRef.current!.getBoundingClientRect();
    // Flip above the button when the panel would run off the bottom, then
    // clamp both axes so it stays wholly on screen either way.
    const below = r.bottom + GAP + PANEL_H <= window.innerHeight;
    setPos({
      left: clamp(r.left, GAP, window.innerWidth - PANEL_W - GAP),
      top: clamp(below ? r.bottom + GAP : r.top - GAP - PANEL_H, GAP, window.innerHeight - PANEL_H - GAP),
    });
    opened.current = value;
    latest.current = value;
    useDocStore.temporal.getState().pause();
  };

  // Close on an outside press or Escape. Both routes go through close(), so
  // the temporal store is never left paused.
  useLayoutEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      const t = e.target as Node;
      if (popRef.current?.contains(t) || btnRef.current?.contains(t)) return;
      close();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      e.stopPropagation();
      close();
    };
    window.addEventListener("pointerdown", onDown);
    window.addEventListener("keydown", onKey, true);
    return () => {
      window.removeEventListener("pointerdown", onDown);
      window.removeEventListener("keydown", onKey, true);
    };
  });

  // Unmounting mid-edit (element deleted, pane collapsed) must not leave undo
  // tracking switched off.
  useEffect(() => () => useDocStore.temporal.getState().resume(), []);

  return (
    <>
      <button
        ref={btnRef}
        type="button"
        className={className}
        style={{ background: value }}
        title={title ?? value}
        onClick={toggle}
      />
      {open &&
        createPortal(
          <div ref={popRef} className={styles.pop} style={pos}>
            <Sketch
              // The value it opened on, not the live one: feeding our own
              // output back in re-derives HSV from hex every move, and that
              // roundtrip loses hue at zero saturation.
              color={toHexa(opened.current)}
              presetColors={false}
              onChange={(c) => {
                latest.current = normalizeColor(c.hexa);
                onChange(latest.current);
              }}
            />
          </div>,
          document.body
        )}
    </>
  );
}
