import {
  createSignal,
  createResource,
  createEffect,
  For,
  Show,
  onMount,
  onCleanup,
} from "solid-js";
import type { Accessor, Component } from "solid-js";
import type { Schema } from "../types/schema";
import { fetchSchema, isMetaTable, resolveTable } from "../utils/schemaHelpers";
import type { RelTarget } from "../utils/schemaHelpers";
import dagre from "dagre";
import TableCard from "./TableCard";
import AddRelationshipModal from "./AddRelationshipModal";
import PreviewModal from "./PreviewModal";
import styles from "./Explorer.module.css";

// ─── Layout constants ────────────────────────────────────────────────────────
const CARD_WIDTH = 240;
const CARD_HEADER_H = 37;
const COL_ROW_H = 22;
const COL_PAD_TOP = 4;
const GRID_COLS = 3;
const GRID_COL_STEP = CARD_WIDTH + 80;
const GRID_ROW_STEP = 360;

type Pos = { x: number; y: number };

/** Use dagre to compute a relationship-aware left-to-right layout. */
function computeDagreLayout(s: Schema): Record<string, Pos> {
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: "LR", nodesep: 60, ranksep: 140, marginx: 24, marginy: 24 });
  g.setDefaultEdgeLabel(() => ({}));

  const tnames = Object.keys(s.Tables).filter((n) => !isMetaTable(n)).sort();
  for (const name of tnames) {
    const colCount = Object.keys(s.Tables[name].Columns).length;
    const h = CARD_HEADER_H + COL_PAD_TOP + colCount * COL_ROW_H + 8;
    g.setNode(name, { width: CARD_WIDTH + 40, height: Math.min(h, 320) });
  }
  for (const rel of s.Relationships ?? []) {
    const from = resolveTable(rel.FromTable, tnames);
    const to = resolveTable(rel.ToTable, tnames);
    if (from && to) g.setEdge(from, to);
  }

  dagre.layout(g);

  const result: Record<string, Pos> = {};
  for (const name of tnames) {
    const node = g.node(name);
    if (node) {
      result[name] = {
        x: (node.x ?? 0) - CARD_WIDTH / 2,
        y: (node.y ?? 0) - ((node.height ?? 200) / 2),
      };
    }
  }
  return result;
}

const Explorer: Component<{ refetchSignal?: Accessor<number> }> = (props) => {
  const [schema, { refetch }] = createResource(fetchSchema);

  // Re-fetch when the parent bumps the signal (e.g. after POST /refresh).
  if (props.refetchSignal !== undefined) {
    let initial = true;
    createEffect(() => {
      props.refetchSignal!();
      if (initial) { initial = false; return; }
      refetch();
    });
  }

  // Absolute positions for each table card on the canvas
  const [positions, setPositions] = createSignal<Record<string, Pos>>({});

  // Modal state
  const [relPrefill, setRelPrefill] = createSignal<Partial<RelTarget> | null>(null);
  const [relEdit, setRelEdit] = createSignal<RelTarget | null>(null);
  const [previewTable, setPreviewTable] = createSignal<string | null>(null);

  // In-progress relationship drag (renders as a dashed SVG line)
  type RelDrag = { fromTable: string; fromCol: string; x1: number; y1: number; x2: number; y2: number };
  const [relDrag, setRelDrag] = createSignal<RelDrag | null>(null);

  // Mutable card-drag ref (not reactive — updated on every mousemove)
  let cardDragRef: { table: string; startMouseX: number; startMouseY: number; startCardX: number; startCardY: number } | null = null;

  // DOM ref for canvas coordinate calculation
  let canvasInnerEl!: HTMLDivElement;

  // Initialise positions when schema first loads; only add newly-seen tables
  createEffect(() => {
    const s = schema();
    if (!s) return;
    const names = Object.keys(s.Tables).filter((n) => !isMetaTable(n)).sort();
    setPositions((prev) => {
      const existing = Object.keys(prev);
      if (existing.length === 0) {
        // First load — use dagre for a relationship-aware layout
        return computeDagreLayout(s);
      }
      // Subsequent: only add new tables below the existing ones
      const missing = names.filter((n) => !(n in prev));
      if (missing.length === 0) return prev;
      const maxY = Math.max(...Object.values(prev).map((p) => p.y), 0);
      const next = { ...prev };
      missing.forEach((name, i) => {
        next[name] = {
          x: 24 + (i % GRID_COLS) * GRID_COL_STEP,
          y: maxY + GRID_ROW_STEP + Math.floor(i / GRID_COLS) * GRID_ROW_STEP,
        };
      });
      return next;
    });
  });

  /** Canvas-space coordinates from a viewport MouseEvent. */
  function canvasCoords(e: MouseEvent): Pos {
    const rect = canvasInnerEl.getBoundingClientRect();
    return { x: e.clientX - rect.left, y: e.clientY - rect.top };
  }

  // ── Global mouse event handlers ────────────────────────────────────────────
  onMount(() => {
    function onMouseMove(e: MouseEvent) {
      if (cardDragRef) {
        const { table, startMouseX, startMouseY, startCardX, startCardY } = cardDragRef;
        setPositions((p) => ({
          ...p,
          [table]: {
            x: Math.max(0, startCardX + e.clientX - startMouseX),
            y: Math.max(0, startCardY + e.clientY - startMouseY),
          },
        }));
      }
      if (relDrag()) {
        const { x, y } = canvasCoords(e);
        setRelDrag((rd) => (rd ? { ...rd, x2: x, y2: y } : rd));
      }
    }

    function onMouseUp(e: MouseEvent) {
      cardDragRef = null;

      const drag = relDrag();
      if (drag) {
        // Walk up from the element under the cursor to find a colRow with data-col
        let cur = document.elementFromPoint(e.clientX, e.clientY) as HTMLElement | null;
        while (cur && !cur.dataset.col) {
          cur = cur.parentElement as HTMLElement | null;
        }
        if (cur?.dataset.col && cur.dataset.table && cur.dataset.table !== drag.fromTable) {
          setRelPrefill({
            FromTable: drag.fromTable,
            FromColumn: drag.fromCol,
            ToTable: cur.dataset.table,
            ToColumn: cur.dataset.col,
          });
        }
        setRelDrag(null);
      }
    }

    document.addEventListener("mousemove", onMouseMove);
    document.addEventListener("mouseup", onMouseUp);
    onCleanup(() => {
      document.removeEventListener("mousemove", onMouseMove);
      document.removeEventListener("mouseup", onMouseUp);
    });
  });

  // ── Card / column drag callbacks ────────────────────────────────────────────
  function onHeaderMouseDown(tableName: string, e: MouseEvent) {
    const pos = positions()[tableName];
    if (!pos) return;
    e.preventDefault();
    cardDragRef = {
      table: tableName,
      startMouseX: e.clientX,
      startMouseY: e.clientY,
      startCardX: pos.x,
      startCardY: pos.y,
    };
  }

  function onColDotMouseDown(tableName: string, colName: string, e: MouseEvent) {
    e.preventDefault();
    // Use the dot element's actual bounding rect for an accurate line start
    const dotEl = e.target as HTMLElement;
    const dotRect = dotEl.getBoundingClientRect();
    const canvasRect = canvasInnerEl.getBoundingClientRect();
    const x1 = dotRect.left + dotRect.width / 2 - canvasRect.left;
    const y1 = dotRect.top + dotRect.height / 2 - canvasRect.top;
    const { x: x2, y: y2 } = canvasCoords(e);
    setRelDrag({ fromTable: tableName, fromCol: colName, x1, y1, x2, y2 });
  }

  // ── DOM-based relationship lines ───────────────────────────────────────────
  // We defer DOM queries to requestAnimationFrame so that <Show>/<For> children
  // are guaranteed to be painted before we call getBoundingClientRect.
  // For columns inside a max-height scrollable card, dots that are scrolled out
  // of view are clamped to the card's visible bounds.
  const [lineData, setLineData] = createSignal<{ key: string; d: string; x1: number; y1: number; x2: number; y2: number }[]>([]);
  const [hoveredRel, setHoveredRel] = createSignal<string | null>(null);
  let lineRaf = 0;

  /** Read a dot's canvas-local centre, clamped to its card's scrollable column area. */
  function dotPos(dot: HTMLElement, canvasRect: DOMRect): Pos {
    const dr = dot.getBoundingClientRect();
    let cx = dr.left + dr.width / 2 - canvasRect.left;
    let cy = dr.top + dr.height / 2 - canvasRect.top;

    // Clamp to the scrollable columns container (not the full card with header)
    const scrollArea = dot.closest("[data-card]")?.querySelector("[data-card-columns]") as HTMLElement | null;
    if (scrollArea) {
      const sr = scrollArea.getBoundingClientRect();
      const areaTop = sr.top - canvasRect.top;
      const areaBot = sr.bottom - canvasRect.top;
      const areaLeft = sr.left - canvasRect.left;
      const areaRight = sr.right - canvasRect.left;
      cy = Math.max(areaTop, Math.min(areaBot, cy));
      cx = Math.max(areaLeft, Math.min(areaRight, cx));
    }
    return { x: cx, y: cy };
  }

  function computeLines() {
    const s = schema();
    if (!s || !canvasInnerEl) { setLineData([]); return; }

    const rels = s.Relationships ?? [];
    const tnames = Object.keys(s.Tables).filter((n) => !isMetaTable(n));
    const canvasRect = canvasInnerEl.getBoundingClientRect();

    const lines = rels.flatMap((rel) => {
      const fromKey = resolveTable(rel.FromTable, tnames);
      const toKey = resolveTable(rel.ToTable, tnames);

      const fromDot = canvasInnerEl.querySelector(
        `[data-dot-table="${CSS.escape(fromKey)}"][data-dot-col="${CSS.escape(rel.FromColumn)}"]`
      ) as HTMLElement | null;
      const toDot = canvasInnerEl.querySelector(
        `[data-dot-table="${CSS.escape(toKey)}"][data-dot-col="${CSS.escape(rel.ToColumn)}"]`
      ) as HTMLElement | null;
      if (!fromDot || !toDot) return [];

      const p1 = dotPos(fromDot, canvasRect);
      const p2 = dotPos(toDot, canvasRect);

      const cxOff = Math.max(60, Math.abs(p2.x - p1.x) / 2);
      const dir = p2.x >= p1.x ? 1 : -1;

      // Encode full rel metadata in the key so we can delete by key alone
      const key = `${rel.FromTable}\0${rel.FromColumn}\0${rel.ToTable}\0${rel.ToColumn}`;

      return [{
        key,
        d: `M${p1.x},${p1.y} C${p1.x + dir * cxOff},${p1.y} ${p2.x - dir * cxOff},${p2.y} ${p2.x},${p2.y}`,
        x1: p1.x, y1: p1.y, x2: p2.x, y2: p2.y,
      }];
    });

    setLineData(lines);
  }

  createEffect(() => {
    const s = schema();
    if (!s) { setLineData([]); return; }
    // Subscribe to positions so lines update whenever a card is dragged
    positions();
    // Defer to next animation frame so <Show>/<For> DOM is painted
    cancelAnimationFrame(lineRaf);
    lineRaf = requestAnimationFrame(computeLines);
  });

  onCleanup(() => cancelAnimationFrame(lineRaf));

  // ── Delete hovered relationship on Del / Backspace ─────────────────────────
  onMount(() => {
    async function onKeyDown(e: KeyboardEvent) {
      if (e.key !== "Delete" && e.key !== "Backspace") return;
      const key = hoveredRel();
      if (!key) return;
      const [fromTable, fromColumn, toTable, toColumn] = key.split("\0");
      await fetch("/relationships", {
        method: "DELETE",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ from_table: fromTable, from_column: fromColumn, to_table: toTable, to_column: toColumn }),
      });
      setHoveredRel(null);
      refetch();
    }
    document.addEventListener("keydown", onKeyDown);
    onCleanup(() => document.removeEventListener("keydown", onKeyDown));
  });

  const tableNames = () => {
    const s = schema();
    if (!s) return [];
    return Object.keys(s.Tables).filter((n) => !isMetaTable(n)).sort();
  };

  return (
    <div class={styles.canvas}>
      <Show when={schema.loading}>
        <div class={styles.status}>Loading schema…</div>
      </Show>
      <Show when={schema.error}>
        <div class={styles.status}>{(schema.error as Error).message}</div>
      </Show>
      <Show when={schema()}>
        <div class={styles.canvasInner} ref={canvasInnerEl}>
          {/* SVG overlay: relationship lines + drag indicator */}
          <svg class={styles.svgOverlay}>
            <defs>
              <For each={lineData()}>
                {(line, i) => (
                  <linearGradient
                    id={`rel-grad-${i()}`}
                    gradientUnits="userSpaceOnUse"
                    x1={line.x1} y1={line.y1}
                    x2={line.x2} y2={line.y2}
                  >
                    <stop offset="0%" stop-color="#fab387" />
                    <stop offset="30%" stop-color="#89b4fa" />
                  </linearGradient>
                )}
              </For>
            </defs>
            <For each={lineData()}>
              {(line, i) => (
                <path
                  class={styles.relPath}
                  classList={{ [styles.relPathHovered]: hoveredRel() === line.key }}
                  d={line.d}
                  stroke={`url(#rel-grad-${i()})`}
                  onMouseEnter={() => setHoveredRel(line.key)}
                  onMouseLeave={() => setHoveredRel((h) => h === line.key ? null : h)}
                  onDblClick={() => {
                    const [ft, fc, tt, tc] = line.key.split("\0");
                    setRelEdit({ FromTable: ft, FromColumn: fc, ToTable: tt, ToColumn: tc });
                  }}
                />
              )}
            </For>
            <Show when={relDrag()}>
              {(rd) => (
                <line
                  class={styles.dragLine}
                  x1={rd().x1}
                  y1={rd().y1}
                  x2={rd().x2}
                  y2={rd().y2}
                />
              )}
            </Show>
          </svg>

          {/* Table cards */}
          <For each={tableNames()}>
            {(name) => {
              const pos = () => positions()[name] ?? { x: 0, y: 0 };
              return (
                <TableCard
                  tableName={name}
                  table={schema()!.Tables[name]}
                  x={pos().x}
                  y={pos().y}
                  onHeaderMouseDown={(e) => onHeaderMouseDown(name, e)}
                  onColDotMouseDown={(e, col) => onColDotMouseDown(name, col, e)}
                  onColumnsScroll={computeLines}
                  onPreview={() => setPreviewTable(name)}
                />
              );
            }}
          </For>
        </div>
      </Show>

      {/* Relationship modal (pre-filled from drag or empty) */}
      <Show when={relPrefill() !== null && schema()}>
        <AddRelationshipModal
          schema={schema()!}
          prefill={relPrefill()!}
          onClose={() => setRelPrefill(null)}
          onSaved={() => {
            setRelPrefill(null);
            refetch();
          }}
        />
      </Show>

      {/* Relationship edit modal (from double-click on line) */}
      <Show when={relEdit() !== null && schema()}>
        <AddRelationshipModal
          schema={schema()!}
          initial={relEdit()!}
          onClose={() => setRelEdit(null)}
          onSaved={() => {
            setRelEdit(null);
            refetch();
          }}
        />
      </Show>

      {/* Table preview modal */}
      <Show when={previewTable()}>
        <PreviewModal
          tableName={previewTable()!}
          onClose={() => setPreviewTable(null)}
        />
      </Show>
    </div>
  );
};

export default Explorer;
