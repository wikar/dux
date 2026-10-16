// Registry dispatch and query lifetime shared by all visualization bodies.
import type { MeasureFormat, QueryResponse } from "@dux/core";
import { useFormats, useResolvedTheme } from "../data";
import { useElementData } from "../elementQuery";
import { useDocStore } from "../store";
import type { DashElement, ElementType } from "../types";
import { displayMessage } from "../message";
import { useVisualData } from "../useVisualData";
import { TYPE_LABEL, VISUALS } from "../visuals";
import type { QueryVisualDef } from "../visuals/types";
import { ElementBoundary } from "./ElementBoundary";
import { FloatingActions } from "./ElementControls";
import VisualSurface from "./VisualSurface";
import styles from "./ElementBody.module.css";

export default function ElementBody({ el }: { el: DashElement }) {
  return (
    <ElementBoundary
      key={el.type}
      fallback={(error) => <Placeholder type={el.type} note={displayMessage(error).split("\n")[0]} />}
    >
      <TypedBody el={el} />
    </ElementBoundary>
  );
}

/** Registry dispatch: query-backed visuals go through the shared data
 *  pipeline, the rest render straight from the element. */
function TypedBody({ el }: { el: DashElement }) {
  const def = VISUALS[el.type];
  if (!def) return <Placeholder type={el.type} note="Unknown type" />;
  if (def.data) return <DataBody el={el} def={def} />;

  const Body = def.Body;
  if (!def.controls) return <Body el={el} />;
  // Controls need a positioned wrapper to float in (the map).
  return (
    <VisualSurface actions={<FloatingActions el={el} def={def} />}>
      <Body el={el} />
    </VisualSurface>
  );
}

function Placeholder({ type, note }: { type: ElementType; note: string }) {
  const def = VISUALS[type];
  return (
    <div className={styles.placeholder}>
      <span className={styles.icon}>{def?.icon}</span>
      <span>{TYPE_LABEL[type] ?? type}</span>
      <span className={styles.hint}>{note}</span>
    </div>
  );
}

// ─── Query-backed bodies ─────────────────────────────────────────────────────

function DataBody({ el, def }: { el: DashElement; def: QueryVisualDef }) {
  const { dux, data, error, loading } = useElementData(el);
  const formats = useFormats();
  const doc = useDocStore((s) => s.doc);
  // One resolved theme covers both the series palette and the chrome a
  // visual draws itself (selected-marker outlines).
  const theme = useResolvedTheme(doc);

  if (!dux) {
    return (
      <div className={styles.placeholder}>
        <span className={styles.icon}>{def.icon}</span>
        <span className={styles.hint}>Add fields in the settings pane</span>
      </div>
    );
  }

  return (
    <VisualSurface loading={loading && !data} error={error}
      actions={data ? <FloatingActions el={el} def={def} /> : null}>
      {data && (
        <PreparedVisual el={el} def={def} data={data} formats={formats} palette={theme.palette} textColor={theme.text} />
      )}
    </VisualSurface>
  );
}

function PreparedVisual({ el, def, data, formats, palette, textColor }: {
  el: DashElement;
  def: QueryVisualDef;
  data: QueryResponse;
  formats: Record<string, MeasureFormat>;
  palette: string[];
  textColor: string;
}) {
  const props = useVisualData(el, def, data, formats, palette, textColor);
  const Body = def.Body;
  return <Body {...props} />;
}
