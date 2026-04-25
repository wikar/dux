import { createMemo, createSignal, createResource, For, Show, onMount } from "solid-js";
import type { Component } from "solid-js";
import type { Schema, DragPayload } from "../types/schema";
import hljs from "highlight.js/lib/core";
import duxLanguage from "../utils/duxLanguage";
import { DUX_KEYWORDS, DUX_BUILTINS } from "../utils/duxKeywords";
import TypeIcon from "./TypeIcon";
import styles from "./SchemaTree.module.css";

hljs.registerLanguage("dux", duxLanguage);

async function fetchSchema(): Promise<Schema> {
  const res = await fetch("/schema");
  if (!res.ok) throw new Error(`schema fetch failed: ${res.status}`);
  return res.json();
}

// Tables whose names start with "dux_" (or come from the "dux" metadata DB)
// are internal — hide from the schema view.
const isMetaTable = (name: string) => {
  const dot = name.indexOf(".");
  if (dot >= 0) return name.slice(0, dot) === "dux";
  return name.startsWith("dux_");
};

// ─── Draggable field row ─────────────────────────────────────────────────────

const FieldRow: Component<{ payload: DragPayload }> = (props) => {
  function handleDragStart(e: DragEvent) {
    e.dataTransfer!.effectAllowed = "copy";
    e.dataTransfer!.setData("application/dux", JSON.stringify(props.payload));
  }

  return (
    <div
      draggable={true}
      class={styles.fieldRow}
      onDragStart={handleDragStart}
    >
      <Show when={props.payload.kind === "measure"} fallback={
        <TypeIcon dataType={props.payload.dataType} />
      }>
        <span class={styles.measureIcon} title="Measure">ƒx</span>
      </Show>
      <span class={styles.fieldName}>{props.payload.name}</span>
    </div>
  );
};

// ─── Collapsible table group ─────────────────────────────────────────────────

const TableGroup: Component<{
  tableName: string;
  schema: Schema;
}> = (props) => {
  const [open, setOpen] = createSignal(false);

  const columns = () => {
    const tbl = props.schema.Tables[props.tableName];
    if (!tbl) return [];
    return Object.values(tbl.Columns).sort((a, b) => a.Name.localeCompare(b.Name));
  };

  const measures = () => {
    const tblMeasures = props.schema.Measures?.[props.tableName];
    if (!tblMeasures) return [];
    return Object.keys(tblMeasures).sort();
  };

  const total = () => columns().length + measures().length;

  return (
    <div class={styles.tableGroup}>
      <button
        class={styles.tableHeader}
        onClick={() => setOpen((v) => !v)}
        title={`${total()} fields`}
      >
        <span class={styles.chevron}>{open() ? "▾" : "▸"}</span>
        <span class={styles.tableName}>{props.tableName}</span>
        <span class={styles.fieldCount}>{total()}</span>
      </button>

      <Show when={open()}>
        <div class={styles.fieldList}>
          <For each={columns()}>
            {(col) => (
              <FieldRow payload={{
                table: props.tableName,
                name: col.Name,
                kind: "column",
                dataType: col.DataType,
              }} />
            )}
          </For>
          <For each={measures()}>
            {(measureName) => (
              <FieldRow payload={{
                table: props.tableName,
                name: measureName,
                kind: "measure",
                dataType: "",
              }} />
            )}
          </For>
        </div>
      </Show>
    </div>
  );
};

// ─── Autocomplete for expression editor ──────────────────────────────────────

function getExprCompletion(text: string, pos: number, schema: Schema | undefined): string {
  if (pos === 0) return "";
  const before = text.slice(0, pos);

  const lastOpen = before.lastIndexOf("[");
  const lastClose = before.lastIndexOf("]");
  if (lastOpen > lastClose) {
    const typed = before.slice(lastOpen + 1);
    const preceding = before.slice(0, lastOpen);
    let contextTable: string | undefined;
    if (schema) {
      const qm = preceding.match(/'([^']+)'\s*$/);
      if (qm) contextTable = Object.keys(schema.Tables).find((t) => t.toLowerCase() === qm[1].toLowerCase());
      if (!contextTable) {
        const bm = preceding.match(/(\w+)\s*$/);
        if (bm) contextTable = Object.keys(schema.Tables).find((t) => t.toLowerCase() === bm[1].toLowerCase());
      }
    }
    const names: string[] = [];
    if (schema) {
      if (contextTable) {
        const tbl = schema.Tables[contextTable];
        if (tbl) for (const c of Object.values(tbl.Columns)) names.push(c.Name);
        const ms = schema.Measures?.[contextTable];
        if (ms) for (const n of Object.keys(ms)) names.push(n);
      } else {
        for (const tbl of Object.values(schema.Tables))
          for (const c of Object.values(tbl.Columns)) names.push(c.Name);
        if (schema.Measures)
          for (const ms of Object.values(schema.Measures))
            for (const n of Object.keys(ms)) names.push(n);
      }
    }
    const upper = typed.toUpperCase();
    const match = names.find((f) =>
      typed.length === 0 ? true : f.toUpperCase().startsWith(upper) && f.toUpperCase() !== upper
    );
    return match ? match.slice(typed.length) + "]" : "";
  }

  if ((before.match(/'/g) || []).length % 2 === 1) {
    const typed = before.slice(before.lastIndexOf("'") + 1);
    const tables = schema ? Object.keys(schema.Tables).filter((n) => !isMetaTable(n)) : [];
    const upper = typed.toUpperCase();
    const match = tables.find((t) =>
      typed.length === 0 ? true : t.toUpperCase().startsWith(upper) && t.toUpperCase() !== upper
    );
    return match ? match.slice(typed.length) + "'" : "";
  }

  let start = pos;
  while (start > 0 && /\w/.test(text[start - 1])) start--;
  const word = text.slice(start, pos);
  if (word.length < 1) return "";
  const upper = word.toUpperCase();
  const tables = schema ? Object.keys(schema.Tables).filter((n) => !isMetaTable(n)) : [];
  const match = [...DUX_KEYWORDS, ...DUX_BUILTINS, ...tables].find(
    (c) => c.toUpperCase().startsWith(upper) && c.toUpperCase() !== upper
  );
  return match ? match.slice(word.length) : "";
}

// ─── Mini DUX expression editor ──────────────────────────────────────────────

const ExprEditor: Component<{
  value: string;
  onChange: (v: string) => void;
  schema: Schema | undefined;
  placeholder?: string;
}> = (props) => {
  let rulerEl!: HTMLSpanElement;
  let charW = 7.5;

  onMount(() => {
    const w = rulerEl?.getBoundingClientRect().width;
    if (w > 0) charW = w;
  });

  const [ghost, setGhost] = createSignal("");
  const [ghostCursor, setGhostCursor] = createSignal(0);
  const [scrollTop, setScrollTop] = createSignal(0);
  const [scrollLeft, setScrollLeft] = createSignal(0);

  const highlighted = createMemo(() =>
    props.value ? hljs.highlight(props.value, { language: "dux" }).value + "\n" : "\n"
  );

  const ghostStyle = createMemo(() => {
    const g = ghost();
    if (!g) return "display:none";
    const before = props.value.slice(0, ghostCursor());
    const lines = before.split("\n");
    const top = 8 + (lines.length - 1) * (12.5 * 1.55) - scrollTop();
    const left = 12 + lines[lines.length - 1].length * charW - scrollLeft();
    return `display:block;top:${top}px;left:${left}px`;
  });

  function refresh(text: string, pos: number) {
    setGhost(getExprCompletion(text, pos, props.schema));
    setGhostCursor(pos);
  }

  function onInput(e: InputEvent) {
    const el = e.currentTarget as HTMLTextAreaElement;
    props.onChange(el.value);
    refresh(el.value, el.selectionStart);
  }

  function onKeyDown(e: KeyboardEvent) {
    const el = e.currentTarget as HTMLTextAreaElement;
    const { selectionStart: start, selectionEnd: end, value: val } = el;
    if (e.key === "Tab") {
      e.preventDefault();
      const g = ghost();
      if (g && start === end) {
        const nv = val.slice(0, start) + g + val.slice(start);
        props.onChange(nv);
        setGhost("");
        requestAnimationFrame(() => {
          el.selectionStart = el.selectionEnd = start + g.length;
          refresh(nv, start + g.length);
        });
      } else {
        setGhost("");
        props.onChange(val.slice(0, start) + "    " + val.slice(end));
        requestAnimationFrame(() => { el.selectionStart = el.selectionEnd = start + 4; });
      }
      return;
    }
    if (e.key === "Escape") setGhost("");
  }

  function onKeyUp(e: KeyboardEvent) {
    if (["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(e.key)) {
      const el = e.currentTarget as HTMLTextAreaElement;
      refresh(el.value, el.selectionStart);
    }
  }

  return (
    <div class={styles.exprWrap}>
      <span ref={rulerEl} class={styles.exprRuler} aria-hidden>m</span>
      <pre
        class={styles.exprHL}
        style={`margin-top:${-scrollTop()}px;margin-left:${-scrollLeft()}px`}
        innerHTML={highlighted()}
        aria-hidden
      />
      <span class={styles.exprGhost} style={ghostStyle()} aria-hidden>{ghost()}</span>
      <textarea
        class={styles.exprTA}
        value={props.value}
        placeholder={props.placeholder}
        spellcheck={false}
        onInput={onInput}
        onKeyDown={onKeyDown}
        onKeyUp={(e) => onKeyUp(e)}
        onScroll={(e) => {
          const ta = e.currentTarget as HTMLTextAreaElement;
          setScrollTop(ta.scrollTop);
          setScrollLeft(ta.scrollLeft);
        }}
        onClick={(e) => {
          const el = e.currentTarget as HTMLTextAreaElement;
          refresh(el.value, el.selectionStart);
        }}
      />
    </div>
  );
};

// ─── Add / edit measure modal ─────────────────────────────────────────────────

type EditTarget = { table: string; name: string; expression: string };

const AddMeasureModal: Component<{
  schema: Schema;
  initial?: EditTarget;
  onClose: () => void;
  onSaved: () => void;
}> = (props) => {
  const isEdit = () => !!props.initial;
  const [table, setTable] = createSignal(props.initial?.table ?? "");
  const [name, setName] = createSignal(props.initial?.name ?? "");
  const [expression, setExpression] = createSignal(props.initial?.expression ?? "");
  const [error, setError] = createSignal("");
  const [saving, setSaving] = createSignal(false);

  const tables = () =>
    Object.keys(props.schema.Tables).filter((n) => !isMetaTable(n)).sort();

  async function save() {
    if (!table() || !name() || !expression()) {
      setError("Table, name, and expression are required.");
      return;
    }
    setSaving(true);
    setError("");
    try {
      const orig = props.initial;
      // In edit mode, if the name changed delete the old entry first.
      if (orig && (orig.table !== table() || orig.name !== name())) {
        await fetch(
          `/measures/${encodeURIComponent(orig.table)}/${encodeURIComponent(orig.name)}`,
          { method: "DELETE" }
        );
      }
      const res = await fetch("/measures", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ table: table(), name: name(), expression: expression() }),
      });
      if (!res.ok) {
        setError(await res.text());
      } else {
        props.onSaved();
      }
    } catch (e) {
      setError(String(e));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div class={styles.modalOverlay} onClick={(e) => { if (e.target === e.currentTarget) props.onClose(); }}>
      <div class={styles.modal}>
        <div class={styles.modalHeader}>
          <span>{isEdit() ? "Edit Measure" : "New Measure"}</span>
          <button class={styles.modalClose} onClick={props.onClose}>✕</button>
        </div>
        <div class={styles.modalBody}>
          <label class={styles.modalLabel}>Table</label>
          <Show
            when={!isEdit()}
            fallback={<div class={styles.modalInput} style="opacity:0.6;cursor:default">{table()}</div>}
          >
            <select
              class={styles.modalSelect}
              value={table()}
              onChange={(e) => setTable((e.currentTarget as HTMLSelectElement).value)}
            >
              <option value="">— select table —</option>
              <For each={tables()}>{(t) => <option value={t}>{t}</option>}</For>
            </select>
          </Show>

          <label class={styles.modalLabel}>Name</label>
          <input
            class={styles.modalInput}
            type="text"
            placeholder="Total Matches"
            value={name()}
            onInput={(e) => setName((e.currentTarget as HTMLInputElement).value)}
          />

          <label class={styles.modalLabel}>Expression</label>
          <ExprEditor
            value={expression()}
            onChange={setExpression}
            schema={props.schema}
            placeholder="COUNT(matches[match_num])"
          />

          <Show when={error()}>
            <div class={styles.modalError}>{error()}</div>
          </Show>
        </div>
        <div class={styles.modalFooter}>
          <button class={styles.modalBtn} onClick={props.onClose} disabled={saving()}>Cancel</button>
          <button
            class={`${styles.modalBtn} ${styles.modalBtnPrimary}`}
            onClick={save}
            disabled={saving()}
          >
            {saving() ? "Saving…" : "Save"}
          </button>
        </div>
      </div>
    </div>
  );
};

// ─── Add relationship modal ───────────────────────────────────────────────────

type RelTarget = { FromTable: string; FromColumn: string; ToTable: string; ToColumn: string };

const AddRelationshipModal: Component<{
  schema: Schema;
  initial?: RelTarget;
  onClose: () => void;
  onSaved: () => void;
}> = (props) => {
  const isEdit = () => !!props.initial;
  const tables = () => Object.keys(props.schema.Tables).filter((n) => !isMetaTable(n)).sort();

  // Relationship FromTable/ToTable may be bare ("matches") while schema.Tables
  // keys are qualified ("atp.matches"). Resolve bare names to their full key.
  const resolveTable = (name: string) => {
    const keys = tables();
    // exact match first
    if (keys.includes(name)) return name;
    // try matching the part after the dot
    const match = keys.find((k) => k.endsWith("." + name));
    return match ?? name;
  };

  const colsFor = (t: string) => {
    const tbl = props.schema.Tables[t];
    return tbl ? Object.values(tbl.Columns).map((c) => c.Name).sort() : [];
  };

  const initFrom = resolveTable(props.initial?.FromTable ?? "");
  const initTo = resolveTable(props.initial?.ToTable ?? "");
  const [fromTable, setFromTable] = createSignal(initFrom);
  const [fromCol, setFromCol] = createSignal(props.initial?.FromColumn ?? "");
  const [toTable, setToTable] = createSignal(initTo);
  const [toCol, setToCol] = createSignal(props.initial?.ToColumn ?? "");
  const [error, setError] = createSignal("");
  const [saving, setSaving] = createSignal(false);

  async function save() {
    if (!fromTable() || !fromCol() || !toTable() || !toCol()) {
      setError("All four fields are required.");
      return;
    }
    setSaving(true);
    setError("");
    try {
      const orig = props.initial;
      if (orig && (
        orig.FromTable !== fromTable() || orig.FromColumn !== fromCol() ||
        orig.ToTable !== toTable() || orig.ToColumn !== toCol()
      )) {
        await fetch("/relationships", {
          method: "DELETE",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            from_table: orig.FromTable, from_column: orig.FromColumn,
            to_table: orig.ToTable, to_column: orig.ToColumn,
          }),
        });
      }
      const res = await fetch("/relationships", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          from_table: fromTable(), from_column: fromCol(),
          to_table: toTable(), to_column: toCol(),
        }),
      });
      if (!res.ok) setError(await res.text());
      else props.onSaved();
    } catch (e) {
      setError(String(e));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div class={styles.modalOverlay} onClick={(e) => { if (e.target === e.currentTarget) props.onClose(); }}>
      <div class={styles.modal}>
        <div class={styles.modalHeader}>
          <span>{isEdit() ? "Edit Relationship" : "New Relationship"}</span>
          <button class={styles.modalClose} onClick={props.onClose}>✕</button>
        </div>
        <div class={styles.modalBody}>
          <label class={styles.modalLabel}>From table</label>
          <select class={styles.modalSelect}
            onChange={(e) => { setFromTable((e.currentTarget as HTMLSelectElement).value); setFromCol(""); }}>
            <option value="">— select table —</option>
            {tables().map(t => <option value={t} selected={t === fromTable()}>{t}</option>)}
          </select>

          <label class={styles.modalLabel}>From column</label>
          <select class={styles.modalSelect}
            onChange={(e) => setFromCol((e.currentTarget as HTMLSelectElement).value)}>
            <option value="">— select column —</option>
            {colsFor(fromTable()).map(c => <option value={c} selected={c === fromCol()}>{c}</option>)}
          </select>

          <label class={styles.modalLabel}>To table</label>
          <select class={styles.modalSelect}
            onChange={(e) => { setToTable((e.currentTarget as HTMLSelectElement).value); setToCol(""); }}>
            <option value="">— select table —</option>
            {tables().map(t => <option value={t} selected={t === toTable()}>{t}</option>)}
          </select>

          <label class={styles.modalLabel}>To column</label>
          <select class={styles.modalSelect}
            onChange={(e) => setToCol((e.currentTarget as HTMLSelectElement).value)}>
            <option value="">— select column —</option>
            {colsFor(toTable()).map(c => <option value={c} selected={c === toCol()}>{c}</option>)}
          </select>

          <Show when={error()}>
            <div class={styles.modalError}>{error()}</div>
          </Show>
        </div>
        <div class={styles.modalFooter}>
          <button class={styles.modalBtn} onClick={props.onClose} disabled={saving()}>Cancel</button>
          <button class={`${styles.modalBtn} ${styles.modalBtnPrimary}`} onClick={save} disabled={saving()}>
            {saving() ? "Saving…" : "Save"}
          </button>
        </div>
      </div>
    </div>
  );
};

// ─── Relationships section ────────────────────────────────────────────────────

const RelationshipsSection: Component<{
  schema: Schema;
  onAdd: () => void;
  onEdit: (r: RelTarget) => void;
  onDelete: (r: RelTarget) => void;
}> = (props) => {
  const [open, setOpen] = createSignal(false);
  const rels = () => props.schema.Relationships ?? [];

  return (
    <div class={styles.sideSection}>
      <button class={styles.sideSectionHeader} onClick={() => setOpen((v) => !v)}>
        <span class={styles.chevron}>{open() ? "▾" : "▸"}</span>
        <span class={styles.sideSectionTitle}>Relationships</span>
        <span class={styles.fieldCount}>{rels().length}</span>
        <span
          class={styles.addBtn}
          title="Add relationship"
          role="button"
          tabIndex={0}
          onClick={(e) => { e.stopPropagation(); props.onAdd(); }}
          onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.stopPropagation(); props.onAdd(); } }}
        >+</span>
      </button>
      <Show when={open()}>
        <div class={styles.sideSectionBody}>
          <Show
            when={rels().length > 0}
            fallback={<div class={styles.sideSectionEmpty}>No relationships defined</div>}
          >
            <For each={rels()}>
              {(r) => (
                <div
                  class={styles.relRow}
                  style="cursor:pointer"
                  onClick={() => props.onEdit(r)}
                >
                  <span class={styles.relLabel}>
                    <span class={styles.relTable}>{r.FromTable}</span>
                    <span class={styles.relCol}>[{r.FromColumn}]</span>
                    <span class={styles.relArrow}>→</span>
                    <span class={styles.relTable}>{r.ToTable}</span>
                    <span class={styles.relCol}>[{r.ToColumn}]</span>
                  </span>
                  <button
                    class={styles.deleteBtn}
                    title="Remove relationship"
                    onClick={(e) => { e.stopPropagation(); props.onDelete(r); }}
                  >−</button>
                </div>
              )}
            </For>
          </Show>
        </div>
      </Show>
    </div>
  );
};

// ─── Measures section ─────────────────────────────────────────────────────────

const MeasuresSection: Component<{
  schema: Schema;
  onAdd: () => void;
  onEdit: (table: string, name: string, expression: string) => void;
  onDelete: (table: string, name: string) => void;
}> = (props) => {
  const [open, setOpen] = createSignal(false);

  type Entry = { table: string; name: string };
  const entries = (): Entry[] => {
    const result: Entry[] = [];
    const measures = props.schema.Measures;
    if (!measures) return result;
    for (const [table, defs] of Object.entries(measures))
      for (const name of Object.keys(defs))
        result.push({ table, name });
    return result.sort((a, b) => a.table.localeCompare(b.table) || a.name.localeCompare(b.name));
  };

  return (
    <div class={styles.sideSection}>
      <button class={styles.sideSectionHeader} onClick={() => setOpen((v) => !v)}>
        <span class={styles.chevron}>{open() ? "▾" : "▸"}</span>
        <span class={styles.sideSectionTitle}>Measures</span>
        <span class={styles.fieldCount}>{entries().length}</span>
        <span
          class={styles.addBtn}
          title="Add measure"
          role="button"
          tabIndex={0}
          onClick={(e) => { e.stopPropagation(); props.onAdd(); }}
          onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.stopPropagation(); props.onAdd(); } }}
        >+</span>
      </button>
      <Show when={open()}>
        <div class={styles.sideSectionBody}>
          <Show
            when={entries().length > 0}
            fallback={<div class={styles.sideSectionEmpty}>No measures defined</div>}
          >
            <For each={entries()}>
              {(m) => (
                <div
                  class={styles.measureRow}
                  onClick={() => {
                    const expr = props.schema.Measures?.[m.table]?.[m.name]?.Expression ?? "";
                    props.onEdit(m.table, m.name, expr);
                  }}
                  style="cursor:pointer"
                >
                  <span class={styles.measureIcon} title="Measure">ƒx</span>
                  <span class={styles.measureName}>{m.table}[{m.name}]</span>
                  <button
                    class={styles.deleteBtn}
                    title="Remove measure"
                    onClick={(e) => { e.stopPropagation(); props.onDelete(m.table, m.name); }}
                  >−</button>
                </div>
              )}
            </For>
          </Show>
        </div>
      </Show>
    </div>
  );
};

// ─── Schema tree panel ───────────────────────────────────────────────────────

type ModalMode = "measure-add" | "measure-edit" | "relationship-add" | "relationship-edit";

const SchemaTree: Component = () => {
  const [schema, { refetch }] = createResource(fetchSchema);
  const [modal, setModal] = createSignal<ModalMode | null>(null);
  const [editTarget, setEditTarget] = createSignal<EditTarget | undefined>(undefined);
  const [relEditTarget, setRelEditTarget] = createSignal<RelTarget | undefined>(undefined);

  const tableNames = () =>
    Object.keys(schema()?.Tables ?? {}).filter((n) => !isMetaTable(n)).sort();

  async function deleteMeasure(table: string, name: string) {
    await fetch(`/measures/${encodeURIComponent(table)}/${encodeURIComponent(name)}`, {
      method: "DELETE",
    });
    refetch();
  }

  async function deleteRelationship(r: { FromTable: string; FromColumn: string; ToTable: string; ToColumn: string }) {
    await fetch("/relationships", {
      method: "DELETE",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        from_table: r.FromTable, from_column: r.FromColumn,
        to_table: r.ToTable, to_column: r.ToColumn,
      }),
    });
    refetch();
  }

  function openMeasureAdd() { setEditTarget(undefined); setModal("measure-add"); }
  function openMeasureEdit(table: string, name: string, expression: string) {
    setEditTarget({ table, name, expression }); setModal("measure-edit");
  }
  function openRelEdit(r: RelTarget) { setRelEditTarget(r); setModal("relationship-edit"); }
  function closeModal() { setModal(null); }
  function saved() { closeModal(); refetch(); }

  return (
    <div class={styles.panel}>
      <div class={styles.panelHeader}>Schema</div>
      <div class={styles.scrollArea}>
        <Show when={schema.loading}>
          <div class={styles.status}>Loading…</div>
        </Show>
        <Show when={schema.error}>
          <div class={styles.statusError}>
            {(schema.error as Error).message}
          </div>
        </Show>
        <Show when={schema()}>
          <For each={tableNames()}>
            {(name) => <TableGroup tableName={name} schema={schema()!} />}
          </For>
        </Show>
      </div>
      <Show when={schema()}>
        <RelationshipsSection
          schema={schema()!}
          onAdd={() => { setRelEditTarget(undefined); setModal("relationship-add"); }}
          onEdit={openRelEdit}
          onDelete={deleteRelationship}
        />
        <MeasuresSection
          schema={schema()!}
          onAdd={openMeasureAdd}
          onEdit={openMeasureEdit}
          onDelete={deleteMeasure}
        />
      </Show>
      <Show when={(modal() === "measure-add" || modal() === "measure-edit") && schema()}>
        <AddMeasureModal
          schema={schema()!}
          initial={editTarget()}
          onClose={closeModal}
          onSaved={saved}
        />
      </Show>
      <Show when={(modal() === "relationship-add" || modal() === "relationship-edit") && schema()}>
        <AddRelationshipModal
          schema={schema()!}
          initial={relEditTarget()}
          onClose={closeModal}
          onSaved={saved}
        />
      </Show>
    </div>
  );
};

export default SchemaTree;
