import { useEffect, useState } from "react";
import type { DragEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { Schema, DragPayload, Relationship } from "@dux/core";
import { isMetaTable, resolveTable, duxClient as client } from "@dux/core";
import TypeIcon from "./TypeIcon";
import styles from "./SchemaTree.module.css";
import AddRelationshipModal from "./AddRelationshipModal";
import { EyeOffIcon } from "./icons";
import AddMeasureModal, { type MeasureTarget } from "./AddMeasureModal";
import TreeCaret from "./TreeCaret";
import { invalidateModelQueries, schemaQuery } from "../model/modelQueries";
import { deleteModelMeasure } from "../model/modelMutations";

// ─── Draggable field row ─────────────────────────────────────────────────────

function FieldRow(props: {
  payload: DragPayload;
  hidden?: boolean;
  onToggleHidden?: () => void;
}) {
  function handleDragStart(e: DragEvent) {
    e.dataTransfer.effectAllowed = "copy";
    e.dataTransfer.setData("application/dux", JSON.stringify(props.payload));
  }

  return (
    <div
      draggable={true}
      className={`${styles.fieldRow}${props.hidden === true ? ` ${styles.fieldRowHidden}` : ""}`}
      onDragStart={handleDragStart}
    >
      {props.payload.kind === "measure" ? (
        <span className={styles.measureIcon} title="Measure">ƒx</span>
      ) : (
        <TypeIcon dataType={props.payload.dataType} />
      )}
      <span className={styles.fieldName}>{props.payload.name}</span>
      {props.onToggleHidden && (
        <button
          className={`${styles.hideBtn}${props.hidden === true ? ` ${styles.hideBtnActive}` : ""}`}
          title={props.hidden ? "Hidden — click to unhide" : "Hide this column"}
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            props.onToggleHidden?.();
          }}
        >
          <EyeOffIcon size={11} />
        </button>
      )}
    </div>
  );
}

// ─── Collapsible table group ─────────────────────────────────────────────────

/** Visible columns and measures of one table, both sorted by name. */
function tableFields(schema: Schema, tableName: string, showHidden?: boolean) {
  const table = schema.Tables[tableName];
  const columns = !table
    ? []
    : Object.values(table.Columns)
        .filter((c) => showHidden || !c.Hidden)
        .sort((a, b) => a.Name.localeCompare(b.Name));

  // Measures may be keyed by bare name ("Sales") or qualified ("analytics.Sales").
  // Try both so qualified table names in the schema panel still find their measures.
  const dot = tableName.indexOf(".");
  const bare = dot >= 0 ? tableName.slice(dot + 1) : tableName;
  const tblMeasures = schema.Measures?.[tableName] ?? schema.Measures?.[bare];
  const measures = tblMeasures ? Object.keys(tblMeasures).sort() : [];

  return { columns, measures };
}

function TableGroup(props: {
  tableName: string;
  schema: Schema;
  columns: ReturnType<typeof tableFields>["columns"];
  measures: string[];
  open: boolean;
  onToggleOpen: () => void;
  onToggleHidden?: () => void;
  onToggleColumnHidden?: (colName: string) => void;
}) {
  const { open, columns, measures } = props;
  const table = props.schema.Tables[props.tableName];
  const isHidden = table?.Hidden === true;
  const total = columns.length + measures.length;

  return (
    <div className={styles.tableGroup}>
      <button
        className={`${styles.tableHeader}${isHidden ? ` ${styles.tableHeaderHidden}` : ""}`}
        onClick={props.onToggleOpen}
        title={`${total} fields`}
      >
        <TreeCaret open={open} />
        <span className={styles.tableName}>{props.tableName}</span>
        {table?.IsView && (
          <span className={styles.viewBadge} title="DuckDB view">view</span>
        )}
        <span className={styles.fieldCount}>{total}</span>
        <span
          className={`${styles.hideBtn}${isHidden ? ` ${styles.hideBtnActive}` : ""}`}
          title={isHidden ? "Hidden — click to unhide" : "Hide this table"}
          role="button"
          tabIndex={0}
          onClick={(e) => { e.stopPropagation(); props.onToggleHidden?.(); }}
          onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.stopPropagation(); props.onToggleHidden?.(); } }}
        >
          <EyeOffIcon size={12} />
        </span>
      </button>

      {open && (
        <div className={styles.fieldList}>
          {columns.map((col) => (
            <FieldRow
              key={col.Name}
              payload={{
                table: props.tableName,
                name: col.Name,
                kind: "column",
                dataType: col.DataType,
              }}
              hidden={col.Hidden === true}
              onToggleHidden={() => props.onToggleColumnHidden?.(col.Name)}
            />
          ))}
          {measures.map((measureName) => (
            <FieldRow
              key={measureName}
              payload={{
                table: props.tableName,
                name: measureName,
                kind: "measure",
                dataType: "",
              }}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// ─── Relationships section ────────────────────────────────────────────────────

function RelationshipsSection(props: {
  schema: Schema;
  onAdd: () => void;
  onEdit: (r: Relationship) => void;
  onDelete: (r: Relationship) => void;
}) {
  const [open, setOpen] = useState(false);
  const rels = props.schema.Relationships ?? [];

  return (
    <div className={styles.sideSection}>
      <button className={styles.sideSectionHeader} onClick={() => setOpen((v) => !v)}>
        <TreeCaret open={open} />
        <span className={styles.sideSectionTitle}>Relationships</span>
        <span className={styles.fieldCount}>{rels.length}</span>
        <span
          className={styles.addBtn}
          title="Add relationship"
          role="button"
          tabIndex={0}
          onClick={(e) => { e.stopPropagation(); props.onAdd(); }}
          onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.stopPropagation(); props.onAdd(); } }}
        >+</span>
      </button>
      {open && (
        <div className={styles.sideSectionBody}>
          {rels.length > 0 ? (
            rels.map((r) => {
              const tkeys = Object.keys(props.schema.Tables).filter((n) => !isMetaTable(n));
              const from = resolveTable(r.FromTable, tkeys);
              const to = resolveTable(r.ToTable, tkeys);
              return (
                <div
                  key={`${r.FromTable}\0${r.FromColumn}\0${r.ToTable}\0${r.ToColumn}`}
                  className={styles.relRow}
                  style={{ cursor: "pointer" }}
                  onClick={() => props.onEdit(r)}
                >
                  <span className={styles.relLabel}>
                    <span className={styles.relTable}>{from}</span>
                    <span className={styles.relCol}>[{r.FromColumn}]</span>
                    <span className={styles.relArrow}>→</span>
                    <span className={styles.relTable}>{to}</span>
                    <span className={styles.relCol}>[{r.ToColumn}]</span>
                  </span>
                  <button
                    className={styles.deleteBtn}
                    title="Remove relationship"
                    onClick={(e) => { e.stopPropagation(); props.onDelete(r); }}
                  >−</button>
                </div>
              );
            })
          ) : (
            <div className={styles.sideSectionEmpty}>No relationships defined</div>
          )}
        </div>
      )}
    </div>
  );
}

// ─── Measures section ─────────────────────────────────────────────────────────

function MeasuresSection(props: {
  schema: Schema;
  onAdd: () => void;
  onEdit: (table: string, name: string, expression: string) => void;
  onDelete: (table: string, name: string) => void;
}) {
  const [open, setOpen] = useState(false);

  type Entry = { table: string; name: string };
  const entries: Entry[] = (() => {
    const result: Entry[] = [];
    const measures = props.schema.Measures;
    if (!measures) return result;
    for (const [table, defs] of Object.entries(measures))
      for (const name of Object.keys(defs))
        result.push({ table, name });
    return result.sort((a, b) => a.table.localeCompare(b.table) || a.name.localeCompare(b.name));
  })();

  return (
    <div className={styles.sideSection}>
      <button className={styles.sideSectionHeader} onClick={() => setOpen((v) => !v)}>
        <TreeCaret open={open} />
        <span className={styles.sideSectionTitle}>Measures</span>
        <span className={styles.fieldCount}>{entries.length}</span>
        <span
          className={styles.addBtn}
          title="Add measure"
          role="button"
          tabIndex={0}
          onClick={(e) => { e.stopPropagation(); props.onAdd(); }}
          onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.stopPropagation(); props.onAdd(); } }}
        >+</span>
      </button>
      {open && (
        <div className={styles.sideSectionBody}>
          {entries.length > 0 ? (
            entries.map((m) => (
              <div
                key={`${m.table}\0${m.name}`}
                className={styles.measureRow}
                onClick={() => {
                  const expr = props.schema.Measures?.[m.table]?.[m.name]?.Expression ?? "";
                  props.onEdit(m.table, m.name, expr);
                }}
                style={{ cursor: "pointer" }}
              >
                <span className={styles.measureIcon} title="Measure">ƒx</span>
                <span className={styles.measureName}>{m.table}[{m.name}]</span>
                <button
                  className={styles.deleteBtn}
                  title="Remove measure"
                  onClick={(e) => { e.stopPropagation(); props.onDelete(m.table, m.name); }}
                >−</button>
              </div>
            ))
          ) : (
            <div className={styles.sideSectionEmpty}>No measures defined</div>
          )}
        </div>
      )}
    </div>
  );
}

// ─── Schema tree panel ───────────────────────────────────────────────────────

type ModalMode = "measure-add" | "measure-edit" | "relationship-add" | "relationship-edit";

export default function SchemaTree(props: {
  showHidden?: boolean;
  /** Render without the panel chrome (host provides header/border, e.g. a CollapsiblePanel). */
  bare?: boolean;
}) {
  const queryClient = useQueryClient();
  const { data: schema, error: schemaError, isFetching: loading, refetch } = useQuery(schemaQuery);

  const [modal, setModal] = useState<ModalMode | null>(null);
  const [editTarget, setEditTarget] = useState<MeasureTarget | undefined>(undefined);
  const [relEditTarget, setRelEditTarget] = useState<Relationship | undefined>(undefined);

  // Expansion: `expanded` is what the user opened by hand; while a search is
  // active a hit expands its table instead, and `searchToggled` records tables
  // the user flipped during that search. Clearing the search therefore restores
  // exactly the hand-expanded set from before it.
  const [search, setSearch] = useState("");
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const [searchToggled, setSearchToggled] = useState<ReadonlySet<string>>(new Set());
  const q = search.trim().toLowerCase();
  useEffect(() => setSearchToggled(new Set()), [q]);

  function flip(set: ReadonlySet<string>, name: string) {
    const next = new Set(set);
    if (!next.delete(name)) next.add(name);
    return next;
  }

  const tableNames = !schema
    ? []
    : Object.keys(schema.Tables)
        .filter((n) => !isMetaTable(n))
        .filter((n) => props.showHidden || !schema.Tables[n].Hidden)
        .sort();

  /** Per-table fields, narrowed to the search hits. A table whose own name
   *  matches keeps all of its fields. */
  const groups = !schema
    ? []
    : tableNames
        .map((name) => {
          const all = tableFields(schema, name, props.showHidden);
          if (!q) return { name, ...all, hit: false };
          if (name.toLowerCase().includes(q)) return { name, ...all, hit: true };
          const columns = all.columns.filter((c) => c.Name.toLowerCase().includes(q));
          const measures = all.measures.filter((m) => m.toLowerCase().includes(q));
          return { name, columns, measures, hit: columns.length + measures.length > 0 };
        })
        .filter((g) => !q || g.hit);

  const [deleteError, setDeleteError] = useState("");

  async function toggle(name: string, col?: string) {
    if (!schema) return;
    try {
      const hidden = col ? schema.Tables[name]?.Columns[col]?.Hidden : schema.Tables[name]?.Hidden;
      if (hidden) await client.clearHidden(name, col);
      else await client.setHidden(name, col);
      setDeleteError("");
      refetch();
    } catch (e) {
      setDeleteError(`Failed to toggle hidden: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  async function deleteMeasure(table: string, name: string) {
    try {
      await deleteModelMeasure(queryClient, table, name);
      setDeleteError("");
    } catch (e) {
      setDeleteError(`Failed to delete measure: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  async function deleteRelationship(r: Relationship) {
    try {
      await client.deleteRelationship({
        from_table: r.FromTable, from_column: r.FromColumn,
        to_table: r.ToTable, to_column: r.ToColumn,
      });
      setDeleteError("");
      refetch();
    } catch (e) {
      setDeleteError(`Failed to delete relationship: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  function openMeasureAdd() { setEditTarget(undefined); setModal("measure-add"); }
  function openMeasureEdit(table: string, name: string, expression: string) {
    const format = schema?.MeasureFormats?.[table]?.[name];
    setEditTarget({ table, name, expression, format });
    setModal("measure-edit");
  }
  function openRelEdit(r: Relationship) { setRelEditTarget(r); setModal("relationship-edit"); }
  function closeModal() { setModal(null); }
  function saved() {
    closeModal();
    void invalidateModelQueries(queryClient);
  }

  return (
    <div className={props.bare ? styles.panelBare : styles.panel}>
      {!props.bare && <div className={styles.panelHeader}>Schema</div>}
      <div className={styles.searchRow}>
        <input
          className={styles.searchInput}
          type="search"
          placeholder="Search columns and measures…"
          value={search}
          onChange={(e) => setSearch(e.currentTarget.value)}
        />
      </div>
      <div className={styles.scrollArea}>
        {loading && <div className={styles.status}>Loading…</div>}
        {schemaError && (
          <div className={styles.statusError}>{schemaError.message}</div>
        )}
        {schema && q !== "" && groups.length === 0 && (
          <div className={styles.status}>No matches</div>
        )}
        {schema && groups.map((g) => (
          <TableGroup
            key={g.name}
            tableName={g.name}
            schema={schema}
            columns={g.columns}
            measures={g.measures}
            open={(q ? g.hit : expanded.has(g.name)) !== searchToggled.has(g.name)}
            onToggleOpen={() =>
              q
                ? setSearchToggled((s) => flip(s, g.name))
                : setExpanded((s) => flip(s, g.name))
            }
            onToggleHidden={() => toggle(g.name)}
            onToggleColumnHidden={(col) => toggle(g.name, col)}
          />
        ))}
      </div>
      {schema && (
        <>
          <RelationshipsSection
            schema={schema}
            onAdd={() => { setRelEditTarget(undefined); setModal("relationship-add"); }}
            onEdit={openRelEdit}
            onDelete={deleteRelationship}
          />
          <MeasuresSection
            schema={schema}
            onAdd={openMeasureAdd}
            onEdit={openMeasureEdit}
            onDelete={deleteMeasure}
          />
        </>
      )}
      {deleteError && (
        <div className={styles.statusError} style={{ padding: "6px 12px", fontSize: 11 }}>{deleteError}</div>
      )}
      {(modal === "measure-add" || modal === "measure-edit") && schema && (
        <AddMeasureModal
          schema={schema}
          initial={editTarget}
          queryClient={queryClient}
          onClose={closeModal}
          onSaved={closeModal}
        />
      )}
      {(modal === "relationship-add" || modal === "relationship-edit") && schema && (
        <AddRelationshipModal
          schema={schema}
          initial={relEditTarget}
          onClose={closeModal}
          onSaved={saved}
        />
      )}
    </div>
  );
}
