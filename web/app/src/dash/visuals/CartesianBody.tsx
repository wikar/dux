// Base body for every cartesian visual. The registry entry supplies the series
// (and whether the layout is orientable / tick-inset); the shared chart frame
// draws the grid, axes, tooltip, legend and cross-filter interaction.
import { CartesianChart } from "../charts/ChartKit";
import type { DataBodyProps } from "./types";

export default function CartesianBody({
  rows,
  keys,
  formats,
  palette,
  textColor,
  viz,
  legend,
  interaction,
  meta,
}: DataBodyProps) {
  const series = meta.series?.({ viz, palette, keys, split: !!interaction.seriesDim }) ?? [];
  return (
    <CartesianChart
      data={rows}
      series={series}
      formats={formats}
      legend={legend}
      interaction={interaction}
      markStroke={textColor}
      horizontal={!!meta.cartesian?.orientable && viz.orientation === "horizontal"}
      insetTicks={meta.cartesian?.insetTicks}
    />
  );
}
