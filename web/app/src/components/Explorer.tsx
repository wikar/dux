import { useCallback, useEffect, useRef, useState } from "react";
import type { MouseEvent as ReactMouseEvent } from "react";
import type { Schema, Relationship } from "@dux/core";
import { isMetaTable, resolveTable, isDateType, duxClient as client } from "@dux/core";
import dagre from "dagre";
import TableCard from "./TableCard";
import AddRelationshipModal from "./AddRelationshipModal";
import PreviewModal from "./PreviewModal";
import styles from "./Explorer.module.css";
import { useFetch } from "../hooks";

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

type RelDrag = { fromTable: string; fromCol: string; x1: number; y1: number; x2: number; y2: number };
type LineDatum = { key: string; d: string; x1: number; y1: number; x2: number; y2: number; bidirectional: boolean };
type CardDrag = { table: string; startMouseX: number; startMouseY: number; startCardX: number; startCardY: number };

export default function Explorer(props: { refreshCount?: number; showHidden?: boolean }) {
  const { data: schema, error: schemaError, loading, refetch } =
    useFetch(() => client.fetchSchema(), [props.refreshCount]);

  // Absolute positions for each table card on the canvas
  const [positions, setPositions] = useState<Record<string, Pos>>({});

  // Modal state
  const [relPrefill, setRelPrefill] = useState<Partial<Relationship> | null>(null);
  const [relEdit, setRelEdit] = useState<Relationship | null>(null);
  const [previewTable, setPreviewTable] = useState<string | null>(null);

  // In-progress relationship drag (renders as a dashed SVG line)
  const [relDrag, setRelDrag] = useState<RelDrag | null>(null);
  const relDragRef = useRef<RelDrag | null>(null);
  relDragRef.current = relDrag;

  // Mutable card-drag ref (not reactive — updated on every mousemove)
  const cardDragRef = useRef<CardDrag | null>(null);

  // DOM ref for canvas coordinate calculation
  const canvasInnerEl = useRef<HTMLDivElement>(null);

  // Latest schema, readable from stable callbacks (global listeners, rAF).
  const schemaRef = useRef<Schema | undefined>(undefined);
  schemaRef.current = schema;

  // Initialise positions when schema first loads; only add newly-seen tables
  useEffect(() => {
    if (!schema) return;
    const names = Object.keys(schema.Tables).filter((n) => !isMetaTable(n)).sort();
    setPositions((prev) => {
      const existing = Object.keys(prev);
      if (existing.length === 0) {
        // First load — use dagre for a relationship-aware layout
        return computeDagreLayout(schema);
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
  }, [schema]);

  /** Canvas-space coordinates from a viewport MouseEvent. */
  const canvasCoords = useCallback((e: { clientX: number; clientY: number }): Pos => {
    const rect = canvasInnerEl.current!.getBoundingClientRect();
    return { x: e.clientX - rect.left, y: e.clientY - rect.top };
  }, []);

  // ── Global mouse event handlers ────────────────────────────────────────────
  useEffect(() => {
    function onMouseMove(e: MouseEvent) {
      if (cardDragRef.current) {
        const { table, startMouseX, startMouseY, startCardX, startCardY } = cardDragRef.current;
        setPositions((p) => ({
          ...p,
          [table]: {
            x: Math.max(0, startCardX + e.clientX - startMouseX),
            y: Math.max(0, startCardY + e.clientY - startMouseY),
          },
        }));
      }
      if (relDragRef.current) {
        const { x, y } = canvasCoords(e);
        setRelDrag((rd) => (rd ? { ...rd, x2: x, y2: y } : rd));
      }
    }

    function onMouseUp(e: MouseEvent) {
      cardDragRef.current = null;

      const drag = relDragRef.current;
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
    return () => {
      document.removeEventListener("mousemove", onMouseMove);
      document.removeEventListener("mouseup", onMouseUp);
    };
  }, [canvasCoords]);

  // ── Card / column drag callbacks ────────────────────────────────────────────
  function onHeaderMouseDown(tableName: string, e: ReactMouseEvent) {
    const pos = positions[tableName];
    if (!pos) return;
    e.preventDefault();
    cardDragRef.current = {
      table: tableName,
      startMouseX: e.clientX,
      startMouseY: e.clientY,
      startCardX: pos.x,
      startCardY: pos.y,
    };
  }

  function onColDotMouseDown(tableName: string, colName: string, e: ReactMouseEvent) {
    e.preventDefault();
    // Use the dot element's actual bounding rect for an accurate line start
    const dotEl = e.target as HTMLElement;
    const dotRect = dotEl.getBoundingClientRect();
    const canvasRect = canvasInnerEl.current!.getBoundingClientRect();
    const x1 = dotRect.left + dotRect.width / 2 - canvasRect.left;
    const y1 = dotRect.top + dotRect.height / 2 - canvasRect.top;
    const { x: x2, y: y2 } = canvasCoords(e);
    setRelDrag({ fromTable: tableName, fromCol: colName, x1, y1, x2, y2 });
  }

  // ── DOM-based relationship lines ───────────────────────────────────────────
  // We defer DOM queries to requestAnimationFrame so the card DOM is painted
  // before we call getBoundingClientRect. For columns inside a max-height
  // scrollable card, dots scrolled out of view are clamped to the card bounds.
  const [lineData, setLineData] = useState<LineDatum[]>([]);
  const [hoveredRel, setHoveredRel] = useState<string | null>(null);
  const hoveredRelRef = useRef<string | null>(null);
  hoveredRelRef.current = hoveredRel;
  const lineRaf = useRef(0);

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

  const computeLines = useCallback(() => {
    const s = schemaRef.current;
    const canvas = canvasInnerEl.current;
    if (!s || !canvas) { setLineData([]); return; }

    const rels = s.Relationships ?? [];
    const tnames = Object.keys(s.Tables).filter((n) => !isMetaTable(n));
    const canvasRect = canvas.getBoundingClientRect();

    const lines = rels.flatMap((rel) => {
      const fromKey = resolveTable(rel.FromTable, tnames);
      const toKey = resolveTable(rel.ToTable, tnames);

      const fromDot = canvas.querySelector(
        `[data-dot-table="${CSS.escape(fromKey)}"][data-dot-col="${CSS.escape(rel.FromColumn)}"]`
      ) as HTMLElement | null;
      const toDot = canvas.querySelector(
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
        bidirectional: rel.Bidirectional ?? false,
      }];
    });

    setLineData(lines);
  }, []);

  useEffect(() => {
    if (!schema) { setLineData([]); return; }
    // Re-run whenever a card is dragged (positions) or hidden cards mount/unmount
    // (showHidden). Defer to next animation frame so the DOM is painted.
    cancelAnimationFrame(lineRaf.current);
    lineRaf.current = requestAnimationFrame(computeLines);
  }, [schema, positions, props.showHidden, computeLines]);

  useEffect(() => () => cancelAnimationFrame(lineRaf.current), []);

  // ── Delete hovered relationship on Del / Backspace ─────────────────────────
  useEffect(() => {
    async function onKeyDown(e: KeyboardEvent) {
      if (e.key !== "Delete" && e.key !== "Backspace") return;
      const key = hoveredRelRef.current;
      if (!key) return;
      const [fromTable, fromColumn, toTable, toColumn] = key.split("\0");
      await client.deleteRelationship({ from_table: fromTable, from_column: fromColumn, to_table: toTable, to_column: toColumn });
      setHoveredRel(null);
      refetch();
    }
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [refetch]);

  const tableNames = !schema
    ? []
    : Object.keys(schema.Tables)
        .filter((n) => !isMetaTable(n))
        .filter((n) => props.showHidden || !schema.Tables[n].Hidden)
        .sort();

  // ── Date-table designation ─────────────────────────────────────────────────
  /** The designated date column when the named table is the model's date table. */
  const dateColumnOf = (name: string): string | null =>
    schema?.DateTables?.[name.toLowerCase()] ?? null;

  /** Toggle a table as the date table. Designating picks the first date column
      (the only one, when there is exactly one); a second click clears it. */
  async function toggleDateTable(name: string) {
    if (!schema) return;
    try {
      if (dateColumnOf(name)) {
        await client.clearDateTable();
      } else {
        const dateCols = Object.values(schema.Tables[name].Columns)
          .filter((c) => isDateType(c.DataType))
          .sort((a, b) => a.Name.localeCompare(b.Name));
        if (dateCols.length === 0) return;
        await client.setDateTable(name, dateCols[0].Name);
      }
      refetch();
    } catch (err) {
      alert((err as Error).message);
    }
  }

  /** Switch the date column within the already-designated date table. */
  async function setDateColumn(name: string, col: string) {
    try {
      await client.setDateTable(name, col);
      refetch();
    } catch (err) {
      alert((err as Error).message);
    }
  }

  // ── Hidden designation ─────────────────────────────────────────────────────
  /** Toggle the hidden flag on a whole table/view. */
  async function toggleHidden(name: string) {
    if (!schema) return;
    try {
      if (schema.Tables[name]?.Hidden) {
        await client.clearHidden(name);
      } else {
        await client.setHidden(name);
      }
      refetch();
    } catch (err) {
      alert((err as Error).message);
    }
  }

  /** Toggle the hidden flag on a single column. */
  async function toggleColumnHidden(name: string, col: string) {
    if (!schema) return;
    try {
      if (schema.Tables[name]?.Columns[col]?.Hidden) {
        await client.clearHidden(name, col);
      } else {
        await client.setHidden(name, col);
      }
      refetch();
    } catch (err) {
      alert((err as Error).message);
    }
  }

  return (
    <div className={styles.canvas}>
      {loading && <div className={styles.status}>Loading schema…</div>}
      {schemaError && <div className={styles.status}>{schemaError.message}</div>}
      {schema && (
        <div className={styles.canvasInner} ref={canvasInnerEl}>
          {/* SVG overlay: relationship lines + drag indicator */}
          <svg className={styles.svgOverlay}>
            <defs>
              {lineData.map((line, i) => (
                <linearGradient
                  key={line.key}
                  id={`rel-grad-${i}`}
                  gradientUnits="userSpaceOnUse"
                  x1={line.x1} y1={line.y1}
                  x2={line.x2} y2={line.y2}
                >
                  {!line.bidirectional && (
                    <>
                      <stop offset="0%"   stopColor="#fab387" />
                      <stop offset="50%"  stopColor="#89b4fa" />
                    </>
                  )}
                  {line.bidirectional && (
                    <>
                      <stop offset="0%"   stopColor="#a6e3a1" />
                      <stop offset="50%"  stopColor="#89b4fa" />
                      <stop offset="100%" stopColor="#a6e3a1" />
                    </>
                  )}
                </linearGradient>
              ))}
            </defs>
            {lineData.map((line, i) => (
              <path
                key={line.key}
                className={`${styles.relPath}${hoveredRel === line.key ? ` ${styles.relPathHovered}` : ""}`}
                d={line.d}
                stroke={`url(#rel-grad-${i})`}
                onMouseEnter={() => setHoveredRel(line.key)}
                onMouseLeave={() => setHoveredRel((h) => (h === line.key ? null : h))}
                onDoubleClick={() => {
                  const [ft, fc, tt, tc] = line.key.split("\0");
                  setRelEdit({ FromTable: ft, FromColumn: fc, ToTable: tt, ToColumn: tc, Bidirectional: line.bidirectional });
                }}
              />
            ))}
            {relDrag && (
              <line
                className={styles.dragLine}
                x1={relDrag.x1}
                y1={relDrag.y1}
                x2={relDrag.x2}
                y2={relDrag.y2}
              />
            )}
          </svg>

          {/* Table cards */}
          {tableNames.map((name) => {
            const pos = positions[name] ?? { x: 0, y: 0 };
            return (
              <TableCard
                key={name}
                tableName={name}
                table={schema.Tables[name]}
                x={pos.x}
                y={pos.y}
                dateColumn={dateColumnOf(name)}
                showHidden={props.showHidden}
                onHeaderMouseDown={(e) => onHeaderMouseDown(name, e)}
                onColDotMouseDown={(e, col) => onColDotMouseDown(name, col, e)}
                onColumnsScroll={computeLines}
                onPreview={() => setPreviewTable(name)}
                onToggleDateTable={() => toggleDateTable(name)}
                onSetDateColumn={(col) => setDateColumn(name, col)}
                onToggleHidden={() => toggleHidden(name)}
                onToggleColumnHidden={(col) => toggleColumnHidden(name, col)}
              />
            );
          })}
        </div>
      )}

      {/* Relationship modal (pre-filled from drag or empty) */}
      {relPrefill !== null && schema && (
        <AddRelationshipModal
          schema={schema}
          prefill={relPrefill}
          onClose={() => setRelPrefill(null)}
          onSaved={() => {
            setRelPrefill(null);
            refetch();
          }}
        />
      )}

      {/* Relationship edit modal (from double-click on line) */}
      {relEdit !== null && schema && (
        <AddRelationshipModal
          schema={schema}
          initial={relEdit}
          onClose={() => setRelEdit(null)}
          onSaved={() => {
            setRelEdit(null);
            refetch();
          }}
        />
      )}

      {/* Table preview modal */}
      {previewTable && (
        <PreviewModal
          tableName={previewTable}
          onClose={() => setPreviewTable(null)}
        />
      )}
    </div>
  );
}
