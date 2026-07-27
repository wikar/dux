import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import styles from "./Slicer.module.css";
import { applySlicerSelection, dropStaleSlicerValues } from "../actions";
import { useSlicerOptions } from "../data";
import { S, stroke } from "../glyphs";
import { useUiStore } from "../store";
import type { DashElement, SlicerSelection } from "../types";
import type { StaticBodyProps, VisualDef } from "./types";

const slicer: VisualDef = {
  type: "slicer",
  label: "Slicer",
  icon: (
    <svg {...S}>
      <path d="M2 4 L16 4 L11 9.5 L11 15 L7 13 L7 9.5 Z" {...stroke} />
    </svg>
  ),
  size: { w: 200, h: 240 },
  controls: { clear: true },
  // The slicer runs its own options query, not the shared element query.
  useFetching: (el) => useSlicerOptions(el).loading,
  seed: () => ({ slicer: { table: "", column: "", kind: "buttons", multi: true } }),
  Body: ({ el }: StaticBodyProps) => <SlicerBody el={el} />,
};

export default slicer;

/** Interactive slicer body. Pointer events stop here so clicking values
 *  never starts a canvas drag; slicers stay live in view AND edit mode. */
function SlicerBody({ el }: { el: DashElement }) {
  const s = el.slicer;
  const sel = useUiStore((st) => st.slicerSelections[el.id]);
  const dropped = useUiStore((st) => st.presetDropped[el.id]);
  useSlicerPreset(el);

  if (!s?.table || !s?.column) {
    return <div className={styles.hint}>Drop a column on the slicer in the settings pane</div>;
  }

  const kind = s.kind ?? "buttons";
  return (
    <div className={styles.root} onPointerDown={(e) => e.stopPropagation()}>
      {dropped && dropped.length > 0 && (
        <div className={styles.stale} title={`No longer in the data: ${dropped.join(", ")}`}>
          {dropped.length} preset value{dropped.length === 1 ? "" : "s"} missing from the data
        </div>
      )}
      {kind === "range" || kind === "daterange" ? (
        <RangeSlicer el={el} sel={sel} date={kind === "daterange"} />
      ) : kind === "dropdown" ? (
        <DropdownSlicer el={el} sel={sel} />
      ) : (
        <ButtonsSlicer el={el} sel={sel} />
      )}
    </div>
  );
}

/** Check the selection this slicer was seeded with (from slicer.default or the
 *  ?f= link) against the data, once — values the column no longer offers are
 *  dropped from the live selection and reported in the body, so a preset can
 *  never silently filter a dashboard by a category that has since been deleted
 *  or renamed.
 *
 *  Option lists cascade, so a value can also go missing because another
 *  slicer's preset excludes it: dropping it is still right — it selects nothing
 *  in the current filter context — and the notice says which values went. */
function useSlicerPreset(el: DashElement) {
  const pending = useUiStore((st) => st.presetPending[el.id] ?? false);
  const { options, loaded } = useSlicerOptions(el);
  const range = el.slicer?.kind === "range" || el.slicer?.kind === "daterange";

  useEffect(() => {
    if (!pending) return;
    // Range bounds are free scalars with no option list to check against.
    if (range) return useUiStore.getState().resolveSlicerPreset(el.id, []);
    // A failed options query leaves the selection alone — the slicer already
    // shows the error, and a transient failure must not discard a preset.
    if (!loaded) return;
    const cur = values(useUiStore.getState().slicerSelections[el.id]);
    dropStaleSlicerValues(
      el.id,
      cur.filter((v) => options.includes(v)),
      cur.filter((v) => !options.includes(v))
    );
  }, [el.id, pending, range, loaded, options]);
}

const values = (sel: SlicerSelection | undefined): string[] =>
  sel?.kind === "values" ? sel.values : [];

/** Toggle semantics shared by buttons and dropdown: multi adds/removes,
 *  single replaces (and clicking the selected value clears). */
function toggleValue(el: DashElement, sel: SlicerSelection | undefined, v: string) {
  const multi = el.slicer?.multi ?? true;
  const cur = values(sel);
  let next: string[];
  if (multi) next = cur.includes(v) ? cur.filter((x) => x !== v) : [...cur, v];
  else next = cur.includes(v) ? [] : [v];
  applySlicerSelection(el.id, next.length > 0 ? { kind: "values", values: next } : null);
}

// ─── Buttons (clickable value pills) ─────────────────────────────────────────

function ButtonsSlicer({ el, sel }: { el: DashElement; sel: SlicerSelection | undefined }) {
  const { options, loading, error } = useSlicerOptions(el);
  const cur = values(sel);
  // Cap the pill count (configurable, default 20) — selected values always
  // stay visible even when they fall outside the cap.
  const limit = el.slicer?.limit ?? 20;
  const shown = options.slice(0, limit);
  for (const v of cur) if (!shown.includes(v) && options.includes(v)) shown.push(v);
  const hidden = options.length - shown.length;

  return (
    <div className={styles.buttonsWrap}>
      <div className={styles.pills}>
        {shown.map((v) => (
          <button
            key={v}
            className={`${styles.pill}${cur.includes(v) ? ` ${styles.pillActive}` : ""}`}
            onClick={() => toggleValue(el, sel, v)}
          >
            {v}
          </button>
        ))}
        {hidden > 0 && (
          <span className={styles.morePill} title="Raise the pill limit in settings, or narrow with other slicers">
            +{hidden} more
          </span>
        )}
        {options.length === 0 && !loading && !error && <span className={styles.hint}>No values</span>}
      </div>
      {error && <div className={styles.error}>{error.message}</div>}
      {/* Clearing lives on the title bar's eraser control — see ClearButton.
          The footer only reports the first load: a refresh keeps the pills on
          screen, and its in-flight state belongs to the header dot. */}
      <div className={styles.footer}>
        {loading && options.length === 0 && <span className={styles.loading}>…</span>}
      </div>
    </div>
  );
}

// ─── Dropdown (searchable, multi-select) ─────────────────────────────────────

function DropdownSlicer({ el, sel }: { el: DashElement; sel: SlicerSelection | undefined }) {
  const { options, loading, error } = useSlicerOptions(el);
  const cur = values(sel);
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const btnRef = useRef<HTMLButtonElement>(null);
  const popRef = useRef<HTMLDivElement>(null);
  const [popPos, setPopPos] = useState<{ left: number; top: number; width: number } | null>(null);

  // The popup is position:fixed so it escapes the element's overflow:hidden;
  // getBoundingClientRect is already in screen space, canvas scale included.
  const toggleOpen = () => {
    if (!open && btnRef.current) {
      const r = btnRef.current.getBoundingClientRect();
      setPopPos({ left: r.left, top: r.bottom + 4, width: Math.max(180, r.width) });
      setSearch("");
    }
    setOpen((o) => !o);
  };

  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      const t = e.target as Node;
      if (popRef.current?.contains(t) || btnRef.current?.contains(t)) return;
      setOpen(false);
    };
    window.addEventListener("pointerdown", onDown);
    return () => window.removeEventListener("pointerdown", onDown);
  }, [open]);

  const label =
    cur.length === 0 ? "All" : cur.length === 1 ? cur[0] : `${cur.length} selected`;
  const term = search.trim().toLowerCase();
  const shown = term ? options.filter((v) => v.toLowerCase().includes(term)) : options;

  return (
    <div className={styles.dropdownWrap}>
      <button ref={btnRef} className={styles.dropdownBtn} onClick={toggleOpen}>
        <span className={styles.dropdownLabel}>{label}</span>
        {cur.length > 0 && (
          <span
            className={styles.dropdownClear}
            title="Clear"
            onClick={(e) => {
              e.stopPropagation();
              applySlicerSelection(el.id, null);
            }}
          >
            ✕
          </span>
        )}
        <span className={styles.caret}>▾</span>
      </button>
      {error && <div className={styles.error}>{error.message}</div>}
      {open &&
        popPos &&
        // Portal to <body>: the canvas ancestor is CSS-transformed (scale),
        // which would hijack position:fixed and strand the popup mid-screen.
        createPortal(
          <div ref={popRef} className={styles.popup} style={popPos} onPointerDown={(e) => e.stopPropagation()}>
            <input
              autoFocus
              className={styles.search}
              placeholder="Search…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
            <div className={styles.optionList}>
              {shown.map((v) => (
                <button
                  key={v}
                  className={styles.option}
                  onClick={() => {
                    toggleValue(el, sel, v);
                    if (!(el.slicer?.multi ?? true)) setOpen(false);
                  }}
                >
                  {(el.slicer?.multi ?? true) && (
                    <span className={`${styles.checkbox}${cur.includes(v) ? ` ${styles.checkboxOn}` : ""}`}>
                      {cur.includes(v) ? "✓" : ""}
                    </span>
                  )}
                  <span className={styles.optionText}>{v}</span>
                </button>
              ))}
              {shown.length === 0 && <div className={styles.hint}>{loading ? "Loading…" : "No matches"}</div>}
            </div>
          </div>,
          document.body
        )}
    </div>
  );
}

// ─── Range / date range ──────────────────────────────────────────────────────

function RangeSlicer({
  el,
  sel,
  date,
}: {
  el: DashElement;
  sel: SlicerSelection | undefined;
  date: boolean;
}) {
  const from = sel?.kind === "range" ? sel.from ?? "" : "";
  const to = sel?.kind === "range" ? sel.to ?? "" : "";

  const commit = (nextFrom: string, nextTo: string) => {
    if (nextFrom === "" && nextTo === "") applySlicerSelection(el.id, null);
    else
      applySlicerSelection(el.id, {
        kind: "range",
        from: nextFrom || undefined,
        to: nextTo || undefined,
      });
  };

  const type = date ? "date" : "number";
  return (
    <div className={styles.rangeWrap}>
      <label className={styles.rangeField}>
        <span>from</span>
        <input
          type={type}
          className={styles.rangeInput}
          key={`f:${from}`}
          defaultValue={from}
          onBlur={(e) => commit(e.target.value, to)}
        />
      </label>
      <label className={styles.rangeField}>
        <span>to</span>
        <input
          type={type}
          className={styles.rangeInput}
          key={`t:${to}`}
          defaultValue={to}
          onBlur={(e) => commit(from, e.target.value)}
        />
      </label>
    </div>
  );
}
