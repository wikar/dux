// Measure display formatting: renders numeric values according to the
// structured format enum stored on measures in the semantic model (M0).
import type { MeasureFormat } from "./types";

/** Format numeric values with locale-aware grouping. Explicit measure formats
 *  override the default number format. Percent values are ratios
 *  (0.153 → "15.3%"). Locale defaults to the browser locale. */
export function formatValue(
  value: string | number | null,
  format: MeasureFormat | undefined,
  locale?: string
): string {
  if (value === null) return "";
  if (typeof value === "string" && value.trim() === "") return value;
  const n = typeof value === "number" ? value : Number(value);
  if (isNaN(n)) return String(value);

  if (!format) {
    try {
      return new Intl.NumberFormat(locale, { maximumFractionDigits: 20 }).format(n);
    } catch {
      return String(value);
    }
  }
  const d = format.decimals;
  const opts: Intl.NumberFormatOptions = {};
  switch (format.kind) {
    case "number":
      opts.maximumFractionDigits = d ?? 0;
      opts.minimumFractionDigits = d ?? 0;
      break;
    case "decimal":
      opts.maximumFractionDigits = d ?? 2;
      opts.minimumFractionDigits = d ?? 2;
      break;
    case "percent":
      opts.style = "percent";
      opts.maximumFractionDigits = d ?? 1;
      opts.minimumFractionDigits = d ?? 1;
      break;
    case "currency":
      opts.style = "currency";
      opts.currency = format.currency || "USD";
      if (d !== undefined) {
        opts.maximumFractionDigits = d;
        opts.minimumFractionDigits = d;
      }
      break;
    case "compact":
      opts.notation = "compact";
      opts.maximumFractionDigits = d ?? 1;
      break;
    default:
      return String(value);
  }
  try {
    return new Intl.NumberFormat(locale, opts).format(n);
  } catch {
    return String(value);
  }
}
