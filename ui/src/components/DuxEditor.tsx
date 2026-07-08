import { createMemo, createSignal, onMount } from "solid-js";
import type { Component } from "solid-js";
import hljs from "highlight.js/lib/core";
import type { Schema } from "dux-client";
import { duxLanguage, getCompletion } from "dux-client";
import styles from "./DuxEditor.module.css";

hljs.registerLanguage("dux", duxLanguage);

/**
 * DUX code editor: syntax-highlighted underlay + transparent textarea overlay,
 * with schema-aware ghost-text autocomplete (Tab to accept, Escape to dismiss).
 *
 * Sizing/border comes from the wrapper class passed via `class`.
 */
const DuxEditor: Component<{
  value: string;
  onChange: (v: string) => void;
  schema: Schema | undefined;
  placeholder?: string;
  /** Extra class for the wrapper — controls layout, sizing, and border. */
  class?: string;
  /** Hide internal dux_* meta tables from completions. */
  excludeMetaTables?: boolean;
}> = (props) => {
  let highlightEl: HTMLPreElement | undefined;
  let rulerEl: HTMLSpanElement | undefined;
  let charW = 7.5; // measured after mount

  const [ghost, setGhost] = createSignal("");
  const [ghostCursor, setGhostCursor] = createSignal(0);
  const [scrollTop, setScrollTop] = createSignal(0);
  const [scrollLeft, setScrollLeft] = createSignal(0);

  onMount(() => {
    const w = rulerEl?.getBoundingClientRect().width;
    if (w && w > 0) charW = w;
  });

  const highlighted = createMemo(() =>
    props.value ? hljs.highlight(props.value, { language: "dux" }).value + "\n" : "\n"
  );

  const ghostStyle = createMemo(() => {
    const g = ghost();
    if (!g) return "display:none";
    const before = props.value.slice(0, ghostCursor());
    const lines = before.split("\n");
    const top = 8 + (lines.length - 1) * (12.5 * 1.55) - scrollTop(); // padTop + line * lineHeight
    const left = 12 + lines[lines.length - 1].length * charW - scrollLeft(); // padLeft + col * charWidth
    return `display:block;top:${top}px;left:${left}px`;
  });

  function refreshGhost(text: string, pos: number) {
    setGhost(getCompletion(text, pos, props.schema, { excludeMetaTables: props.excludeMetaTables }));
    setGhostCursor(pos);
  }

  function onInput(e: InputEvent) {
    const el = e.currentTarget as HTMLTextAreaElement;
    props.onChange(el.value);
    refreshGhost(el.value, el.selectionStart);
  }

  function onKeyDown(e: KeyboardEvent) {
    const el = e.currentTarget as HTMLTextAreaElement;
    const { selectionStart: start, selectionEnd: end, value: val } = el;

    // Tab: accept ghost suggestion, or fall back to indent
    if (e.key === "Tab") {
      e.preventDefault();
      const g = ghost();
      if (g && start === end) {
        const nv = val.slice(0, start) + g + val.slice(start);
        const newPos = start + g.length;
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
    <div class={props.class ? `${styles.wrap} ${props.class}` : styles.wrap}>
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
        value={props.value}
        placeholder={props.placeholder}
        spellcheck={false}
        onInput={onInput}
        onKeyDown={onKeyDown}
        onKeyUp={onKeyUp}
        onScroll={syncScroll}
        onClick={(e) => {
          const el = e.currentTarget as HTMLTextAreaElement;
          refreshGhost(el.value, el.selectionStart);
        }}
      />
    </div>
  );
};

export default DuxEditor;
