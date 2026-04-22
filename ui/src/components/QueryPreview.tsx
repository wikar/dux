import { createMemo, createSignal, createResource, onMount } from "solid-js";
import type { Component } from "solid-js";
import hljs from "highlight.js/lib/core";
import type { Schema } from "../types/schema";
import duxLanguage from "../utils/duxLanguage";
import styles from "./QueryPreview.module.css";

hljs.registerLanguage("dux", duxLanguage);

// ─── Autocomplete candidates ─────────────────────────────────────────────────

const KEYWORDS = [
  "DEFINE", "EVALUATE", "MEASURE", "RETURN", "VAR",
  "AND", "OR", "NOT", "TRUE", "FALSE",
];

const BUILTINS = [
  "SUM", "AVERAGE", "COUNT", "COUNTA", "COUNTBLANK", "COUNTROWS", "DISTINCTCOUNT",
  "MIN", "MAX", "MEDIAN", "SUMX", "AVERAGEX", "COUNTX", "MINX", "MAXX", "CONCATENATEX",
  "SUMMARIZECOLUMNS", "FILTER", "CALCULATE", "ADDCOLUMNS", "SELECTCOLUMNS",
  "TOPN", "UNION", "INTERSECT", "EXCEPT", "VALUES", "DISTINCT", "TREATAS",
  "DIVIDE", "ISBLANK", "BLANK", "IF", "SWITCH",
];

async function fetchSchema(): Promise<Schema> {
  const res = await fetch("/schema");
  if (!res.ok) throw new Error(`schema fetch failed: ${res.status}`);
  return res.json();
}

// ─── Completion logic ─────────────────────────────────────────────────────────

/**
 * Returns the ghost-text suffix to append at the cursor position.
 * Empty string = no suggestion.
 *
 * For [ and ' contexts the ghost always includes the closing char so accepting
 * it (Tab) produces the full token.
 */
function getCompletion(text: string, pos: number, schema: Schema | undefined): string {
  if (pos === 0) return "";
  const before = text.slice(0, pos);

  // Inside [ ... ] → complete a field / measure name
  const lastBracketOpen = before.lastIndexOf("[");
  const lastBracketClose = before.lastIndexOf("]");
  if (lastBracketOpen > lastBracketClose) {
    const typed = before.slice(lastBracketOpen + 1);

    // Detect an immediately preceding table name: word chars or 'Quoted Name'
    const precedingToken = before.slice(0, lastBracketOpen);
    let contextTable: string | undefined;
    if (schema) {
      // Try 'Quoted Table Name'[
      const quotedMatch = precedingToken.match(/'([^']+)'\s*$/);
      if (quotedMatch) {
        const name = quotedMatch[1];
        contextTable = Object.keys(schema.Tables).find(
          (t) => t.toLowerCase() === name.toLowerCase()
        );
      }
      // Try bare word identifier[
      if (!contextTable) {
        const bareMatch = precedingToken.match(/(\w+)\s*$/);
        if (bareMatch) {
          const name = bareMatch[1];
          contextTable = Object.keys(schema.Tables).find(
            (t) => t.toLowerCase() === name.toLowerCase()
          );
        }
      }
    }

    const fieldNames: string[] = [];
    if (schema) {
      if (contextTable) {
        // Only fields from the matched table
        const tbl = schema.Tables[contextTable];
        if (tbl) for (const col of Object.values(tbl.Columns)) fieldNames.push(col.Name);
        const measures = schema.Measures?.[contextTable];
        if (measures) for (const name of Object.keys(measures)) fieldNames.push(name);
      } else {
        // No table context — all fields
        for (const table of Object.values(schema.Tables))
          for (const col of Object.values(table.Columns)) fieldNames.push(col.Name);
        if (schema.Measures)
          for (const measures of Object.values(schema.Measures))
            for (const name of Object.keys(measures)) fieldNames.push(name);
      }
    }

    const upper = typed.toUpperCase();
    const match = fieldNames.find((f) =>
      typed.length === 0
        ? true
        : f.toUpperCase().startsWith(upper) && f.toUpperCase() !== upper
    );
    if (!match) return "";
    return match.slice(typed.length) + "]";
  }

  // Inside ' ... ' → complete a table name
  const quotesBefore = (before.match(/'/g) || []).length;
  if (quotesBefore % 2 === 1) {
    const lastQuote = before.lastIndexOf("'");
    const typed = before.slice(lastQuote + 1);
    const tableNames = schema ? Object.keys(schema.Tables) : [];
    const upper = typed.toUpperCase();
    const match = tableNames.find((t) =>
      typed.length === 0
        ? true
        : t.toUpperCase().startsWith(upper) && t.toUpperCase() !== upper
    );
    if (!match) return "";
    return match.slice(typed.length) + "'";
  }

  // Plain identifier → keyword / built-in / table name
  let start = pos;
  while (start > 0 && /\w/.test(text[start - 1])) start--;
  const word = text.slice(start, pos);
  if (word.length < 1) return "";
  const upper = word.toUpperCase();
  const tableNames = schema ? Object.keys(schema.Tables) : [];
  const match = [...KEYWORDS, ...BUILTINS, ...tableNames].find(
    (c) => c.toUpperCase().startsWith(upper) && c.toUpperCase() !== upper
  );
  return match ? match.slice(word.length) : "";
}

// ─── Cursor pixel position ────────────────────────────────────────────────────

function cursorPixelPos(text: string, pos: number, charW: number) {
  const before = text.slice(0, pos);
  const lines = before.split("\n");
  const lineIdx = lines.length - 1;
  const colIdx = lines[lineIdx].length;
  return {
    top: 8 + lineIdx * (12.5 * 1.55),  // padTop + line * lineHeight
    left: 12 + colIdx * charW,          // padLeft + col * charWidth
  };
}

// ─── Component ────────────────────────────────────────────────────────────────

const QueryPreview: Component<{
  query: string;
  isDirty: boolean;
  onQueryChange: (q: string) => void;
  onRun: () => void;
}> = (props) => {
  let highlightEl: HTMLPreElement | undefined;
  let rulerEl: HTMLSpanElement | undefined;
  let charW = 7.5; // measured after mount

  const [schema] = createResource(fetchSchema);
  const [ghost, setGhost] = createSignal("");
  const [ghostCursor, setGhostCursor] = createSignal(0);
  const [scrollTop, setScrollTop] = createSignal(0);
  const [scrollLeft, setScrollLeft] = createSignal(0);

  onMount(() => {
    if (rulerEl) {
      const w = rulerEl.getBoundingClientRect().width;
      if (w > 0) charW = w;
    }
  });

  const highlighted = createMemo(() =>
    props.query ? hljs.highlight(props.query, { language: "dux" }).value : ""
  );

  const ghostStyle = createMemo(() => {
    const g = ghost();
    if (!g) return "display:none";
    const { top, left } = cursorPixelPos(props.query, ghostCursor(), charW);
    return `display:block;top:${top - scrollTop()}px;left:${left - scrollLeft()}px`;
  });

  function refreshGhost(text: string, pos: number) {
    setGhost(getCompletion(text, pos, schema()));
    setGhostCursor(pos);
  }

  function onInput(e: InputEvent) {
    const el = e.currentTarget as HTMLTextAreaElement;
    props.onQueryChange(el.value);
    refreshGhost(el.value, el.selectionStart);
  }

  function onKeyDown(e: KeyboardEvent) {
    const el = e.currentTarget as HTMLTextAreaElement;
    const start = el.selectionStart;
    const end = el.selectionEnd;
    const val = el.value;

    // Tab: accept ghost suggestion, or fall back to indent
    if (e.key === "Tab") {
      e.preventDefault();
      const g = ghost();
      if (g && start === end) {
        const nv = val.slice(0, start) + g + val.slice(start);
        const newPos = start + g.length;
        props.onQueryChange(nv);
        setGhost("");
        requestAnimationFrame(() => {
          el.selectionStart = el.selectionEnd = newPos;
          refreshGhost(nv, newPos);
        });
      } else {
        setGhost("");
        const nv = val.slice(0, start) + "    " + val.slice(end);
        props.onQueryChange(nv);
        requestAnimationFrame(() => { el.selectionStart = el.selectionEnd = start + 4; });
      }
      return;
    }

    // Escape: dismiss ghost
    if (e.key === "Escape") {
      setGhost("");
    }
  }

  function onKeyUp(e: KeyboardEvent) {
    if (["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(e.key)) {
      const el = e.currentTarget as HTMLTextAreaElement;
      refreshGhost(el.value, el.selectionStart);
    }
  }

  function syncScroll(e: Event) {
    const ta = e.currentTarget as HTMLTextAreaElement;
    if (highlightEl) {
      highlightEl.scrollTop = ta.scrollTop;
      highlightEl.scrollLeft = ta.scrollLeft;
    }
    setScrollTop(ta.scrollTop);
    setScrollLeft(ta.scrollLeft);
  }

  return (
    <div class={styles.panel}>
      <div class={styles.header}>Query</div>
      <div class={styles.codeWrapper}>
        <pre
          ref={highlightEl}
          class={styles.highlight}
          aria-hidden="true"
          innerHTML={highlighted()}
        />
        {/* Hidden ruler — measures monospace char width after mount */}
        <span ref={rulerEl} class={styles.ruler} aria-hidden="true">M</span>
        {/* Inline ghost-text suggestion */}
        <span class={styles.ghost} style={ghostStyle()} aria-hidden="true">{ghost()}</span>
        <textarea
          class={styles.code}
          value={props.query}
          placeholder="// Drop fields to build a query"
          spellcheck={false}
          onInput={onInput}
          onKeyDown={onKeyDown}
          onKeyUp={onKeyUp}
          onScroll={syncScroll}
        />
      </div>
      <div class={styles.toolbar}>
        <button
          class={`${styles.runBtn} ${props.isDirty ? styles.runBtnActive : ""}`}
          title={props.isDirty ? "Run query" : "Query up to date"}
          onClick={props.onRun}
        >
          <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
            <path d="M3 2.5l10 5.5-10 5.5V2.5z" />
          </svg>
          Run
        </button>
      </div>
    </div>
  );
};

export default QueryPreview;
