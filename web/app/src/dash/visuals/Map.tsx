// Map: a MapLibre canvas with one or two data layers.
//
// The registry entry stays static — the palette, the settings pane and inserting
// an element all need it — but the body carries the MapLibre engine and its
// stylesheet, which most dashboards never use. Loading it on first render keeps
// that weight out of the dash bundle.
import { lazy, Suspense } from "react";
import styles from "../components/ElementBody.module.css";
import { S, stroke } from "../glyphs";
import type { StaticBodyProps, VisualDef } from "./types";

const MapBody = lazy(() => import("./MapBody"));

const mapVisual: VisualDef = {
  type: "map",
  label: "Map",
  icon: (
    <svg {...S}>
      <path
        d="M9 1.5 C6 1.5 3.8 3.7 3.8 6.6 C3.8 10.5 9 16.5 9 16.5 C9 16.5 14.2 10.5 14.2 6.6 C14.2 3.7 12 1.5 9 1.5 Z"
        {...stroke}
        strokeLinejoin="round"
      />
      <circle cx="9" cy="6.6" r="1.8" fill="currentColor" />
    </svg>
  ),
  size: { w: 420, h: 320 },
  controls: { funnel: true },
  // The map paints its own surface edge to edge.
  bare: true,
  // Layers are fetched per layer rather than through the shared query
  // pipeline, so the map carries filters without a `data` spec.
  seed: () => ({
    query: { mode: "builder", filters: [] },
    viz: { layers: [{ id: "layer-1", kind: "circle" }] },
  }),
  // A failed chunk load throws past Suspense into the element's error boundary,
  // which is the same place a broken body lands.
  Body: ({ el }: StaticBodyProps) => (
    <Suspense
      fallback={
        <div className={styles.overlay}>
          <div className={styles.spinner} />
        </div>
      }
    >
      <MapBody el={el} />
    </Suspense>
  ),
};

export default mapVisual;
