import { isNumeric } from "@dux/core";
import type { DragPayload } from "@dux/core";
import type { DashElement, MapLayer, MapLayerKind } from "../types";
import { addMapLayer, removeMapLayer, setMapLayerField, setMapLayerKind, type MapFieldSlot } from "../docOps";
import { IgnoreSlicers, DropTarget } from "./SettingsDropTarget";
import { FiltersWell } from "./QuerySettings";
import styles from "./ElementsPane.module.css";

const MAP_SLOTS: { slot: MapFieldSlot; label: string; hint: string }[] = [
  { slot: "lng", label: "Longitude", hint: "Drop a numeric column" },
  { slot: "lat", label: "Latitude", hint: "Drop a numeric column" },
  { slot: "size", label: "Size", hint: "Optional measure or numeric column" },
  { slot: "category", label: "Category", hint: "Optional column used for filtering" },
];

export default function MapSettings({ el }: { el: DashElement }) {
  const layers = el.viz?.layers ?? [];
  const accepts = (slot: MapFieldSlot, p: DragPayload) => {
    if (slot === "size") return p.kind === "measure" || isNumeric(p.dataType);
    if (p.kind !== "column") return false;
    return slot === "category" || isNumeric(p.dataType);
  };

  const fieldChip = (layer: MapLayer, slot: MapFieldSlot) => {
    const field = layer[slot];
    if (!field) return undefined;
    return (
      <div className={styles.fieldChip}>
        <span className={styles.fieldChipName}>{field.table}[{field.name}]</span>
        <button className={styles.fieldChipRemove} title="Remove" onClick={() => setMapLayerField(el.id, layer.id, slot, null)}>×</button>
      </div>
    );
  };

  return (
    <div className={styles.section}>
      <div className={styles.heading}>Map</div>
      {layers.map((layer, index) => (
        <div key={layer.id}>
          <label className={styles.label}>Layer {index + 1}</label>
          <div className={styles.row}>
            <select
              className={styles.input}
              value={layer.kind}
              onChange={(e) => setMapLayerKind(el.id, layer.id, e.target.value as MapLayerKind)}
            >
              <option value="circle">Circle</option>
              <option value="pin">Pin</option>
              <option value="heatmap">Heatmap</option>
            </select>
            <button className={styles.btnDanger} onClick={() => removeMapLayer(el.id, layer.id)}>Remove</button>
          </div>
          {MAP_SLOTS.map(({ slot, label, hint }) => (
            <DropTarget
              key={slot}
              label={label}
              hint={hint}
              onDrop={(p) => accepts(slot, p) && setMapLayerField(el.id, layer.id, slot, p)}
            >
              {fieldChip(layer, slot)}
            </DropTarget>
          ))}
        </div>
      ))}
      <button className={styles.btn} disabled={layers.length >= 2} onClick={() => addMapLayer(el.id)}>Add layer</button>

      <button
        className={styles.btn}
        title="Save the map's current center and zoom as this visual's default view"
        onClick={() => window.dispatchEvent(new CustomEvent("dux-map-save-view", { detail: el.id }))}
      >
        Use current view
      </button>
      {el.viz?.center && <div className={styles.hint}>{el.viz.center.map((v) => v.toFixed(4)).join(", ")} · zoom {(el.viz.zoom ?? 1.2).toFixed(1)}</div>}
      <FiltersWell el={el} />
      <IgnoreSlicers el={el} />
    </div>
  );
}

