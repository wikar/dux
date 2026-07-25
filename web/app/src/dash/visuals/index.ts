// The visual registry. Each visual is one self-contained module declaring its
// icon, label, default size and title, seeded fields, field wells, display
// options, header controls and body; this file is only the roll-up.
//
// Adding a visual: add the module, register it below, and add the type to
// ../types.ts and dash/schema.json (the persisted contract).
import type { ElementType } from "../types";
import areaChart from "./AreaChart";
import barChart from "./BarChart";
import comboChart from "./ComboChart";
import donutChart from "./DonutChart";
import imageBox from "./ImageBox";
import kpiCard from "./KpiCard";
import lineChart from "./LineChart";
import mapVisual from "./Map";
import pivotTable from "./PivotTable";
import slicer from "./Slicer";
import table from "./Table";
import textBox from "./TextBox";
import type { VisualDef } from "./types";

/** Key order is the Elements palette order. */
export const VISUALS: Record<ElementType, VisualDef> = {
  bar: barChart,
  line: lineChart,
  combo: comboChart,
  area: areaChart,
  donut: donutChart,
  table: table,
  pivot: pivotTable,
  kpi: kpiCard,
  slicer: slicer,
  text: textBox,
  image: imageBox,
  map: mapVisual,
};

export const VISUAL_TYPES = Object.keys(VISUALS) as ElementType[];

export const TYPE_LABEL: Record<ElementType, string> = Object.fromEntries(
  VISUAL_TYPES.map((t) => [t, VISUALS[t].label])
) as Record<ElementType, string>;

/** Element types whose body renders a DUX query result. */
export const QUERY_TYPES: ReadonlySet<ElementType> = new Set(VISUAL_TYPES.filter((t) => VISUALS[t].data));
