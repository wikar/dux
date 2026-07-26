// Color plumbing shared by the theme editor and the color picker.
// Committed values are normalised to #rrggbb, or rgba(r, g, b, a) when the
// alpha channel is set.

export function parseColor(c: string): { r: number; g: number; b: number; a: number } | null {
  const s = c.trim();
  // Hex, forgiving about what a paste actually carries: the leading # is
  // optional and 3/4-digit shorthand expands, so a value copied out of a
  // palette page or a stylesheet lands as-is.
  let m = /^#?([0-9a-f]{3,4}|[0-9a-f]{6}|[0-9a-f]{8})$/i.exec(s);
  if (m) {
    const h = m[1];
    const wide = h.length > 4 ? h : [...h].map((d) => d + d).join("");
    const n = parseInt(wide.slice(0, 6), 16);
    return {
      r: (n >> 16) & 255,
      g: (n >> 8) & 255,
      b: n & 255,
      a: wide.length === 8 ? parseInt(wide.slice(6), 16) / 255 : 1,
    };
  }
  m = /^rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)(?:[,\s/]+([\d.]+%?))?\s*\)$/i.exec(s);
  if (m) {
    let a = 1;
    if (m[4] !== undefined) a = m[4].endsWith("%") ? parseFloat(m[4]) / 100 : parseFloat(m[4]);
    return { r: +m[1], g: +m[2], b: +m[3], a };
  }
  return null;
}

const hex2 = (n: number) => Math.max(0, Math.min(255, Math.round(n))).toString(16).padStart(2, "0");

/** #rrggbbaa — the one shape the Sketch panel reads without losing alpha.
 *  Unparseable colors fall back to a neutral base. */
export function toHexa(c: string): string {
  const p = parseColor(c) ?? { r: 30, g: 30, b: 46, a: 1 };
  return `#${hex2(p.r)}${hex2(p.g)}${hex2(p.b)}${hex2(p.a * 255)}`;
}

/** Normalise a committed color: rgba only when the alpha channel is set. */
export function normalizeColor(v: string): string {
  const p = parseColor(v);
  if (!p) return v;
  if (p.a >= 1) return `#${hex2(p.r)}${hex2(p.g)}${hex2(p.b)}`;
  return `rgba(${p.r}, ${p.g}, ${p.b}, ${Math.round(p.a * 1000) / 1000})`;
}

/** Re-emit a color with a different alpha, keeping its RGB. */
export function withAlpha(color: string, a: number): string {
  const p = parseColor(color);
  if (!p || Number.isNaN(a)) return color;
  return normalizeColor(`rgba(${p.r}, ${p.g}, ${p.b}, ${Math.min(1, Math.max(0, a))})`);
}
