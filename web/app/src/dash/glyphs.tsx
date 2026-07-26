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

/** Eraser glyph for the slicer's "clear selection" header control. */
export const eraserIcon: JSX.Element = (
  <svg {...S}>
    <path d="M7.6 14.5 L3.2 10.1 A1 1 0 0 1 3.2 8.7 L9.4 2.5 A1 1 0 0 1 10.8 2.5 L15.2 6.9 A1 1 0 0 1 15.2 8.3 L9 14.5 Z"
      {...stroke} strokeLinejoin="round" />
    <path d="M6.3 5.6 L12.1 11.4" {...stroke} />
    <path d="M7.6 14.5 L15.5 14.5" {...stroke} strokeLinecap="round" />
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
