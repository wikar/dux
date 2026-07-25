// KPI card: one measure as a single scalar.
import { formatValue } from "@dux/core";
import styles from "../components/ElementBody.module.css";
import { S } from "../glyphs";
import type { DataBodyProps, VisualDef } from "./types";

function KpiBody({ data, metricCols, formats }: DataBodyProps) {
  const col = metricCols[0] ?? data.columns[data.columns.length - 1];
  const idx = data.columns.indexOf(col);
  const value = idx >= 0 && data.rows.length > 0 ? data.rows[0][idx] : null;
  return (
    <div className={styles.kpi}>
      <span className={styles.kpiValue}>
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
  data: { wells: [{ id: "values", label: "Value", max: 1 }], dropEmpty: false },
  Body: KpiBody,
};

export default kpiCard;
