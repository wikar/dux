import { useMemo } from "react";
import type { MeasureFormat, QueryResponse } from "@dux/core";
import { prepareVisualData, type ChartDim } from "./charts/chartData";
import { elementSeriesSplit } from "./elementQuery";
import { markKey, useUiStore } from "./store";
import type { DashElement } from "./types";
import type { DataBodyProps, QueryVisualDef } from "./visuals/types";

/** Shared preparation and cross-filter wiring; bodies only render. */
export function useVisualData(
  el: DashElement,
  def: QueryVisualDef,
  rawData: QueryResponse,
  formats: Record<string, MeasureFormat>,
  palette: string[],
  textColor: string,
): DataBodyProps {
  const mode = useUiStore((s) => s.mode);
  const crossSel = useUiStore((s) => s.crossFilters[el.id]);
  const toggleCrossMark = useUiStore((s) => s.toggleCrossMark);
  const splitBy = elementSeriesSplit(el);
  const spec = def.data;
  const { crossFilterable, seriesDim, ...prepared } = useMemo(
    () => prepareVisualData(el, spec, rawData, formats, splitBy),
    [el, spec, rawData, formats, splitBy],
  );
  const selectedKeys = useMemo(() => new Set((crossSel ?? []).map(markKey)), [crossSel]);
  const onMarkClick = mode === "view" && crossFilterable
    ? (dims: ChartDim[], additive: boolean) => {
        if (dims.length > 0) toggleCrossMark(el.id, { dims }, additive);
      }
    : undefined;
  return {
    ...prepared,
    el,
    viz: el.viz ?? {},
    palette,
    textColor,
    legend: el.viz?.legend ?? prepared.keys.length > 1,
    interaction: { onMarkClick, selectedKeys, seriesDim },
  };
}
