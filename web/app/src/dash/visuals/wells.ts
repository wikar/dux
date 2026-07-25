// Field-well membership. An element's query.fields is one flat ordered list
// (dims before metrics); wells are views over it, with the secondary metric
// wells and the pivot columns recorded in viz.y2 / viz.lines / viz.cols and the
// series split in viz.series.
import { isMetricField } from "@dux/core";
import type { Aggregate, DropField } from "@dux/core";
import type { BuilderFieldRef, DashElement } from "../types";
import type { WellId } from "./types";

export function asDropField(f: BuilderFieldRef): DropField {
  return {
    table: f.table,
    name: f.name,
    kind: f.kind ?? "column",
    dataType: f.dataType ?? "",
    aggregate: f.aggregate as Aggregate | undefined,
  };
}

export function wellMembers(el: DashElement, well: WellId): BuilderFieldRef[] {
  const fields = el.query?.fields ?? [];
  const isMetric = (f: BuilderFieldRef) => isMetricField(asDropField(f));
  const metrics = fields.filter(isMetric);
  const y2 = new Set(el.viz?.y2 ?? []);
  const lines = new Set(el.viz?.lines ?? []);
  const cols = new Set(el.viz?.cols ?? []);
  const series = el.viz?.series;
  switch (well) {
    case "axis":
      return fields.filter((f) => !isMetric(f) && f.name !== series);
    case "series":
      return fields.filter((f) => !isMetric(f) && f.name === series);
    case "fields":
      return fields;
    case "values":
      // Only a line splits its metrics across two axes; a converted element
      // keeps its old viz.y2, which must not hide metrics elsewhere.
      return el.type === "line" ? metrics.filter((f) => !y2.has(f.name)) : metrics;
    case "y2":
      return metrics.filter((f) => y2.has(f.name));
    case "bars":
      return metrics.filter((f) => !lines.has(f.name));
    case "lines":
      return metrics.filter((f) => lines.has(f.name));
    case "rows":
      return fields.filter((f) => !isMetric(f) && !cols.has(f.name));
    case "cols":
      return fields.filter((f) => !isMetric(f) && cols.has(f.name));
  }
}
