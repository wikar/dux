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

/** Magnitude suffixes, largest first. Thousands are **T**, not K. Trillions
 *  would collide with that T, so the scale stops at B and keeps counting
 *  there (1e12 → "1,000B") rather than inventing a notation. */
const SCALES = [
  { div: 1e9, suffix: "B" },
  { div: 1e6, suffix: "M" },
  { div: 1e3, suffix: "T" },
] as const;

/** Currency affixes for a locale, so a scaled number can be rebuilt with the
 *  symbol in the right place ("€15M" vs "15M €"). */
function currencyAffixes(currency: string, locale?: string): [string, string] {
  try {
    const parts = new Intl.NumberFormat(locale, {
      style: "currency",
      currency,
      maximumFractionDigits: 0,
    }).formatToParts(1);
    const i = parts.findIndex((p) => p.type === "integer");
    return [
      parts.slice(0, i).map((p) => p.value).join(""),
      parts.slice(i + 1).filter((p) => p.type !== "decimal" && p.type !== "fraction").map((p) => p.value).join(""),
    ];
  } catch {
    return ["", ""];
  }
}

/**
 * Axis-scale rendering: the same value `formatValue` spells out in full, cut
 * down to something that fits a tick label — 15300000 → "€15.3M".
 *
 * Percentages and values under a thousand fall through to `formatValue`:
 * they are already short, and scaling a ratio would be nonsense.
 */
export function formatCompactValue(
  value: string | number | null,
  format: MeasureFormat | undefined,
  locale?: string
): string {
  if (value === null) return "";
  const n = typeof value === "number" ? value : Number(value);
  if (isNaN(n)) return formatValue(value, format, locale);
  if (format?.kind === "percent") return formatValue(value, format, locale);

  const abs = Math.abs(n);
  const scale = SCALES.find((s) => abs >= s.div);
  if (!scale) return formatValue(value, format, locale);

  const scaled = Math.abs(n) / scale.div;
  // The sign sits outside the currency affix ("-€15.3M", not "€-15.3M"), so
  // the magnitude is formatted unsigned and the sign put back on the front.
  const sign = n < 0 ? "-" : "";
  // One decimal below 100 keeps 15.3M distinguishable from 15.8M; above that
  // the extra digit is noise on an axis.
  const digits = scaled < 100 ? 1 : 0;
  try {
    const num = new Intl.NumberFormat(locale, {
      maximumFractionDigits: digits,
      minimumFractionDigits: 0,
    }).format(scaled);
    if (format?.kind === "currency" && format.currency) {
      const [pre, post] = currencyAffixes(format.currency, locale);
      return `${sign}${pre}${num}${scale.suffix}${post}`;
    }
    return `${sign}${num}${scale.suffix}`;
  } catch {
    return formatValue(value, format, locale);
  }
}
