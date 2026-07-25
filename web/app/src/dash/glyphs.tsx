// Shared SVG bits for the dashboard chrome. A visual's palette glyph lives in
// its own module (visuals/*.tsx); only the element-header controls are here.
import type { JSX } from "react";

/** Standard 18×18 glyph frame — spread onto every dash <svg>. */
export const S = { width: 18, height: 18, viewBox: "0 0 18 18" };
/** Outline style shared by the stroked glyphs. */
export const stroke = { fill: "none", stroke: "currentColor", strokeWidth: 1.5 } as const;

/** Funnel glyph for the "filters affecting this visual" header control. */
export const funnelIcon: JSX.Element = (
  <svg {...S}>
    <path d="M2.5 4 L15.5 4 L10.5 10 L10.5 15 L7.5 13 L7.5 10 Z" {...stroke} strokeLinejoin="round" />
  </svg>
);

/** Download glyph for the CSV-export header control (matches funnelIcon weight). */
export const downloadIcon: JSX.Element = (
  <svg {...S}>
    <path d="M9 3 L9 11" {...stroke} strokeLinecap="round" />
    <path d="M5.5 8 L9 11.5 L12.5 8" {...stroke} strokeLinecap="round" strokeLinejoin="round" />
    <path d="M3.5 12.5 L3.5 15 L14.5 15 L14.5 12.5" {...stroke} strokeLinecap="round" strokeLinejoin="round" />
  </svg>
);
