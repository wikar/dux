import { useEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties, ChangeEvent, KeyboardEvent, MouseEvent, UIEvent } from "react";
import hljs from "highlight.js/lib/core";
import type { Schema, QueryFailedError } from "@dux/core";
import { duxLanguage, getCompletion } from "@dux/core";
import styles from "./DuxEditor.module.css";

hljs.registerLanguage("dux", duxLanguage);

/**
 * DUX code editor: syntax-highlighted underlay + transparent textarea overlay,
 * with schema-aware ghost-text autocomplete (Tab to accept, Escape to dismiss).
 *
 * Sizing/border comes from the wrapper class passed via `className`.
 */
export default function DuxEditor(props: {
  value: string;
  onChange: (v: string) => void;
  schema: Schema | undefined;
  placeholder?: string;
  /** Extra class for the wrapper — controls layout, sizing, and border. */
  className?: string;
  /** Hide internal dux_* meta tables from completions. */
  excludeMetaTables?: boolean;
  /** Query failure to mark at its 1-based source position (line 0 = no marker). */
  error?: QueryFailedError | null;
}) {
  const highlightEl = useRef<HTMLPreElement>(null);
  const rulerEl = useRef<HTMLSpanElement>(null);
  const charW = useRef(7.5); // measured after mount

  const [ghost, setGhost] = useState("");
  const [ghostCursor, setGhostCursor] = useState(0);
  const [scrollTop, setScrollTop] = useState(0);
  const [scrollLeft, setScrollLeft] = useState(0);

  useEffect(() => {
    const w = rulerEl.current?.getBoundingClientRect().width;
    if (w && w > 0) charW.current = w;
  }, []);

  const highlighted = useMemo(
    () => (props.value ? hljs.highlight(props.value, { language: "dux" }).value + "\n" : "\n"),
    [props.value]
  );

  const ghostStyle = useMemo<CSSProperties>(() => {
    if (!ghost) return { display: "none" };
    const before = props.value.slice(0, ghostCursor);
    const lines = before.split("\n");
    const top = 8 + (lines.length - 1) * (12.5 * 1.55) - scrollTop; // padTop + line * lineHeight
    const left = 12 + lines[lines.length - 1].length * charW.current - scrollLeft; // padLeft + col * charWidth
    return { display: "block", top, left };
  }, [ghost, ghostCursor, props.value, scrollTop, scrollLeft]);

  // Red underline from the error's column to the end of that line.
  const errorStyle = useMemo<CSSProperties>(() => {
    const e = props.error;
    if (!e || e.line <= 0) return { display: "none" };
    const lineText = props.value.split("\n")[e.line - 1] ?? "";
    const col = Math.min(Math.max(e.column, 1), lineText.length + 1);
    const chars = Math.max(lineText.length - (col - 1), 2);
    const top = 8 + (e.line - 1) * (12.5 * 1.55) + 15 - scrollTop; // baseline of the line
    const left = 12 + (col - 1) * charW.current - scrollLeft;
    return { display: "block", top, left, width: chars * charW.current };
  }, [props.error, props.value, scrollTop, scrollLeft]);

  function refreshGhost(text: string, pos: number) {
    setGhost(getCompletion(text, pos, props.schema, { excludeMetaTables: props.excludeMetaTables }));
    setGhostCursor(pos);
  }

  function onChange(e: ChangeEvent<HTMLTextAreaElement>) {
    const el = e.currentTarget;
    props.onChange(el.value);
    refreshGhost(el.value, el.selectionStart);
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    const el = e.currentTarget;
    const { selectionStart: start, selectionEnd: end, value: val } = el;

    // Tab: accept ghost suggestion, or fall back to indent
    if (e.key === "Tab") {
      e.preventDefault();
      if (ghost && start === end) {
        const nv = val.slice(0, start) + ghost + val.slice(start);
        const newPos = start + ghost.length;
        props.onChange(nv);
        setGhost("");
        requestAnimationFrame(() => {
          el.selectionStart = el.selectionEnd = newPos;
          refreshGhost(nv, newPos);
        });
      } else {
        setGhost("");
        props.onChange(val.slice(0, start) + "    " + val.slice(end));
        requestAnimationFrame(() => { el.selectionStart = el.selectionEnd = start + 4; });
      }
      return;
    }

    // Escape: dismiss ghost
    if (e.key === "Escape") setGhost("");
  }

  function onKeyUp(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(e.key)) {
      const el = e.currentTarget;
      refreshGhost(el.value, el.selectionStart);
    }
  }

  function syncScroll(e: UIEvent<HTMLTextAreaElement>) {
    const ta = e.currentTarget;
    if (highlightEl.current) {
      highlightEl.current.scrollTop = ta.scrollTop;
      highlightEl.current.scrollLeft = ta.scrollLeft;
    }
    setScrollTop(ta.scrollTop);
    setScrollLeft(ta.scrollLeft);
  }

  return (
    <div className={props.className ? `${styles.wrap} ${props.className}` : styles.wrap}>
      <pre
        ref={highlightEl}
        className={styles.highlight}
        aria-hidden="true"
        dangerouslySetInnerHTML={{ __html: highlighted }}
      />
      {/* Hidden ruler — measures monospace char width after mount */}
      <span ref={rulerEl} className={styles.ruler} aria-hidden="true">M</span>
      {/* Inline ghost-text suggestion */}
      <span className={styles.ghost} style={ghostStyle} aria-hidden="true">{ghost}</span>
      {/* Error position marker */}
      <span
        className={styles.errorMark}
        style={errorStyle}
        title={props.error?.message}
      />
      <textarea
        className={styles.code}
        value={props.value}
        placeholder={props.placeholder}
        spellCheck={false}
        onChange={onChange}
        onKeyDown={onKeyDown}
        onKeyUp={onKeyUp}
        onScroll={syncScroll}
        onClick={(e: MouseEvent<HTMLTextAreaElement>) => {
          const el = e.currentTarget;
          refreshGhost(el.value, el.selectionStart);
        }}
      />
    </div>
  );
}
