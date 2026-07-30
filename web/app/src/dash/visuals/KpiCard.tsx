// KPI card: one measure as a single scalar.
import { formatValue } from "@dux/core";
import styles from "../components/ElementBody.module.css";
import { S } from "../glyphs";
import type { DataBodyProps, VisualDef } from "./types";

function KpiBody({ data, metricCols, formats, viz }: DataBodyProps) {
  const col = metricCols[0] ?? data.columns[data.columns.length - 1];
  const idx = data.columns.indexOf(col);
  const value = idx >= 0 && data.rows.length > 0 ? data.rows[0][idx] : null;
  const number = Number(value);
  let color: string | undefined;
  if (value !== null && value !== undefined && viz.colorByThreshold && Number.isFinite(number)) {
    const low = Math.min(Number(viz.lowerThreshold ?? 0), Number(viz.upperThreshold ?? 0));
    const high = Math.max(Number(viz.lowerThreshold ?? 0), Number(viz.upperThreshold ?? 0));
    const higherPositive = (viz.positiveDirection ?? "higher") === "higher";
    color = number > high
      ? `var(--th-${higherPositive ? "positive" : "negative"})`
      : number < low
        ? `var(--th-${higherPositive ? "negative" : "positive"})`
        : "var(--th-neutral)";
  }
  return (
    <div className={styles.kpi}>
      <span className={styles.kpiValue} style={{ color }}>
        {value === null || value === undefined ? "—" : formatValue(value, formats[col])}
      </span>
      <span className={styles.kpiLabel}>{col}</span>
    </div>
  );
}

const kpiCard: VisualDef = {
  type: "kpi",
  label: "KPI card",
  icon: (
    <svg {...S}>
      <rect x="2" y="4" width="14" height="10" rx="2" fill="none" stroke="currentColor" strokeWidth="1.5" />
      <text x="9" y="12" textAnchor="middle" fontSize="7" fontWeight="bold" fill="currentColor">
        42
      </text>
    </svg>
  ),
  size: { w: 200, h: 112 },
  controls: { funnel: true },
  // A single scalar has no axis items to drop, and no rows to export.
  data: { wells: [{ id: "values", label: "Value", max: 1 }], dropEmpty: false, sortable: false },
  options: [
    { key: "transparent", label: "Transparent background", kind: "check", default: false },
    { key: "colorByThreshold", label: "Color by thresholds", kind: "check", default: false },
    {
      key: "positiveDirection", label: "Positive values", kind: "select", default: "higher",
      choices: [
        { value: "higher", label: "Above upper threshold" },
        { value: "lower", label: "Below lower threshold" },
      ],
    },
    { key: "upperThreshold", label: "Upper threshold", kind: "number", default: 0 },
    { key: "lowerThreshold", label: "Lower threshold", kind: "number", default: 0 },
  ],
  Body: KpiBody,
};

export default kpiCard;
