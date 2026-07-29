import { useCallback, useEffect, useRef, useState } from "react";
import type { MouseEvent as ReactMouseEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import type { Schema, Relationship } from "@dux/core";
import { isMetaTable, resolveTable, isDateType, duxClient as client } from "@dux/core";
import TableCard from "./TableCard";
import AddRelationshipModal from "./AddRelationshipModal";
import PreviewModal from "./PreviewModal";
import styles from "./Explorer.module.css";

// ─── Layout constants ────────────────────────────────────────────────────────
const CARD_WIDTH = 240;
const GRID_COLS = 3;
const GRID_COL_STEP = CARD_WIDTH + 80;
// Rows clear a full-height card (its columns area caps out around 356) with room
// to spare, so a line between vertically adjacent cards is not a stub.
const GRID_ROW_STEP = 430;

type Pos = { x: number; y: number };

/** Seed layout: cards in a fixed column grid. Positions are the starting
 *  point only — cards stay user-draggable afterwards. */
function computeGridLayout(s: Schema): Record<string, Pos> {
  const tnames = Object.keys(s.Tables).filter((n) => !isMetaTable(n)).sort();
  const result: Record<string, Pos> = {};
  tnames.forEach((name, i) => {
    result[name] = {
      x: 24 + (i % GRID_COLS) * GRID_COL_STEP,
      y: 24 + Math.floor(i / GRID_COLS) * GRID_ROW_STEP,
    };
  });
  return result;
}

type RelDrag = { fromTable: string; fromCol: string; x1: number; y1: number; x2: number; y2: number };
type LineDatum = { key: string; label: string; d: string; x1: number; y1: number; x2: number; y2: number; bidirectional: boolean };

// ─── Relationship line anchors ───────────────────────────────────────────────

/** Card bounds in canvas space, with the centre precomputed. */
type Rect = { left: number; top: number; right: number; bottom: number; cx: number; cy: number };

type Side = "left" | "right" | "top" | "bottom";

/** Where one end of a relationship line attaches: a side of the whole card. */
type EndPoint = { table: string; side: Side; rect: Rect };

const NORMAL: Record<Side, { x: number; y: number }> = {
  left: { x: -1, y: 0 },
  right: { x: 1, y: 0 },
  top: { x: 0, y: -1 },
  bottom: { x: 0, y: 1 },
};

/** Which side of `rect` faces `other` — the side a ray between the two centres
 *  exits through. Both offsets are weighed against the card's own proportions so
 *  a wide card does not spuriously prefer its top and bottom edges. */
function sideToward(rect: Rect, other: Rect): Side {
  const w = rect.right - rect.left;
  const h = rect.bottom - rect.top;
  const dx = other.cx - rect.cx;
  const dy = other.cy - rect.cy;
  return Math.abs(dx) * h > Math.abs(dy) * w
    ? dx > 0 ? "right" : "left"
    : dy > 0 ? "bottom" : "top";
}
type CardDrag = { table: string; startMouseX: number; startMouseY: number; startCardX: number; startCardY: number };

export default function Explorer(props: { showHidden?: boolean }) {
  const { data: schema, error: schemaError, isFetching: loading, refetch } = useQuery({
    queryKey: ["schema"],
    queryFn: () => client.fetchSchema(),
  });

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
        // First load — seed a simple column grid
        return computeGridLayout(schema);
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
  // Lines join whole cards, not individual columns: an end attaches to the side
  // of the card that faces the other table. Which columns a line relates is in
  // its tooltip and its edit dialog. We defer the DOM reads to
  // requestAnimationFrame so the cards are painted before getBoundingClientRect.
  const [lineData, setLineData] = useState<LineDatum[]>([]);
  const [hoveredRel, setHoveredRel] = useState<string | null>(null);
  const hoveredRelRef = useRef<string | null>(null);
  hoveredRelRef.current = hoveredRel;
  const lineRaf = useRef(0);

  const computeLines = useCallback(() => {
    const s = schemaRef.current;
    const canvas = canvasInnerEl.current;
    if (!s || !canvas) { setLineData([]); return; }

    const rels = s.Relationships ?? [];
    const tnames = Object.keys(s.Tables).filter((n) => !isMetaTable(n));
    const canvasRect = canvas.getBoundingClientRect();

    const cardRect = (table: string): Rect | null => {
      const el = canvas.querySelector(`[data-card="${CSS.escape(table)}"]`) as HTMLElement | null;
      if (!el) return null;
      const r = el.getBoundingClientRect();
      const left = r.left - canvasRect.left;
      const top = r.top - canvasRect.top;
      const right = r.right - canvasRect.left;
      const bottom = r.bottom - canvasRect.top;
      return { left, top, right, bottom, cx: (left + right) / 2, cy: (top + bottom) / 2 };
    };

    // Pass 1: pick the side of each card that faces the other one. A hidden
    // table has no card to attach to, so its lines are dropped.
    const pending = rels.flatMap((rel) => {
      const fromKey = resolveTable(rel.FromTable, tnames);
      const toKey = resolveTable(rel.ToTable, tnames);
      const fromRect = cardRect(fromKey);
      const toRect = cardRect(toKey);
      if (!fromRect || !toRect) return [];

      return [{
        rel, fromKey, toKey,
        from: { table: fromKey, rect: fromRect, side: sideToward(fromRect, toRect) },
        to: { table: toKey, rect: toRect, side: sideToward(toRect, fromRect) },
      }];
    });

    // Pass 2: every end lands on a card edge, so any two relationships reaching
    // the same side of the same card would draw on top of each other. Count the
    // ends sharing an edge and fan them out along it; a single occupant sits at
    // the midpoint.
    const edgeTotal = new Map<string, number>();
    for (const p of pending) {
      for (const e of [p.from, p.to]) {
        const k = `${e.table}\0${e.side}`;
        edgeTotal.set(k, (edgeTotal.get(k) ?? 0) + 1);
      }
    }
    const edgeSeen = new Map<string, number>();
    const place = (e: EndPoint): Pos => {
      const k = `${e.table}\0${e.side}`;
      const i = edgeSeen.get(k) ?? 0;
      edgeSeen.set(k, i + 1);
      const f = (i + 1) / ((edgeTotal.get(k) ?? 1) + 1);
      const { rect, side } = e;
      const alongY = rect.top + (rect.bottom - rect.top) * f;
      const alongX = rect.left + (rect.right - rect.left) * f;
      switch (side) {
        case "left": return { x: rect.left, y: alongY };
        case "right": return { x: rect.right, y: alongY };
        case "top": return { x: alongX, y: rect.top };
        case "bottom": return { x: alongX, y: rect.bottom };
      }
    };

    // Pass 3: curve each line out of its end along that side's outward normal,
    // so it leaves the card square to the edge it attaches to.
    const lines = pending.map(({ rel, fromKey, toKey, from, to }) => {
      const p1 = place(from);
      const p2 = place(to);
      const dx = p2.x - p1.x;
      const dy = p2.y - p1.y;

      const n1 = NORMAL[from.side];
      const n2 = NORMAL[to.side];
      // The 60 gives short lines a visible curve, but it has to yield to the gap
      // itself: stacked cards sit nearly flush, and a fixed floor there would
      // throw the control points past both ends and kink the line.
      const floor = Math.min(60, Math.hypot(dx, dy) / 2);
      const off1 = Math.max(floor, Math.abs(n1.x * dx + n1.y * dy) / 2);
      const off2 = Math.max(floor, Math.abs(n2.x * -dx + n2.y * -dy) / 2);
      const c1 = { x: p1.x + n1.x * off1, y: p1.y + n1.y * off1 };
      const c2 = { x: p2.x + n2.x * off2, y: p2.y + n2.y * off2 };

      // Encode full rel metadata in the key so we can delete by key alone
      const key = `${rel.FromTable}\0${rel.FromColumn}\0${rel.ToTable}\0${rel.ToColumn}`;

      return {
        key,
        // An edge-anchored end does not show which column it is, so name both.
        label: `${fromKey}[${rel.FromColumn}] → ${toKey}[${rel.ToColumn}]`,
        d: `M${p1.x},${p1.y} C${c1.x},${c1.y} ${c2.x},${c2.y} ${p2.x},${p2.y}`,
        x1: p1.x, y1: p1.y, x2: p2.x, y2: p2.y,
        bidirectional: rel.Bidirectional ?? false,
      };
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
  /** Toggle the hidden flag on a whole table/view, or a single column. */
  async function toggle(name: string, col?: string) {
    if (!schema) return;
    try {
      const hidden = col ? schema.Tables[name]?.Columns[col]?.Hidden : schema.Tables[name]?.Hidden;
      if (hidden) await client.clearHidden(name, col);
      else await client.setHidden(name, col);
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
              >
                <title>{line.label}</title>
              </path>
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
                onPreview={() => setPreviewTable(name)}
                onToggleDateTable={() => toggleDateTable(name)}
                onSetDateColumn={(col) => setDateColumn(name, col)}
                onToggleHidden={() => toggle(name)}
                onToggleColumnHidden={(col) => toggle(name, col)}
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
