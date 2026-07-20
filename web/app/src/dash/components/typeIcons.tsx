import type { JSX } from "react";
import type { ElementType } from "../types";

const S = { width: 18, height: 18, viewBox: "0 0 18 18" };
const stroke = { fill: "none", stroke: "currentColor", strokeWidth: 1.5 } as const;

const ICONS: Record<ElementType, JSX.Element> = {
  bar: (
    <svg {...S}>
      <rect x="2" y="9" width="3.4" height="7" fill="currentColor" />
      <rect x="7.3" y="4" width="3.4" height="12" fill="currentColor" />
      <rect x="12.6" y="7" width="3.4" height="9" fill="currentColor" />
    </svg>
  ),
  line: (
    <svg {...S}>
      <polyline points="2,13 6.5,7 10.5,10 16,3" {...stroke} />
    </svg>
  ),
  area: (
    <svg {...S}>
      <path d="M2 15 L2 11 L6.5 6 L10.5 9 L16 3 L16 15 Z" fill="currentColor" opacity="0.75" />
    </svg>
  ),
  donut: (
    <svg {...S}>
      <circle cx="9" cy="9" r="6" {...stroke} strokeWidth="3.5" />
    </svg>
  ),
  table: (
    <svg {...S}>
      <rect x="2" y="3" width="14" height="12" {...stroke} />
      <line x1="2" y1="7" x2="16" y2="7" {...stroke} />
      <line x1="9" y1="3" x2="9" y2="15" {...stroke} />
    </svg>
  ),
  pivot: (
    <svg {...S}>
      <rect x="2" y="3" width="14" height="12" {...stroke} />
      <line x1="2" y1="7" x2="16" y2="7" {...stroke} />
      <line x1="7" y1="3" x2="7" y2="15" {...stroke} />
      <rect x="2" y="3" width="5" height="4" fill="currentColor" opacity="0.6" />
    </svg>
  ),
  kpi: (
    <svg {...S}>
      <rect x="2" y="4" width="14" height="10" rx="2" {...stroke} />
      <text x="9" y="12" textAnchor="middle" fontSize="7" fontWeight="bold" fill="currentColor">
        42
      </text>
    </svg>
  ),
  slicer: (
    <svg {...S}>
      <path d="M2 4 L16 4 L11 9.5 L11 15 L7 13 L7 9.5 Z" {...stroke} />
    </svg>
  ),
  text: (
    <svg {...S}>
      <text x="9" y="13.5" textAnchor="middle" fontSize="13" fontWeight="bold" fill="currentColor">
        T
      </text>
    </svg>
  ),
  combo: (
    <svg {...S}>
      <rect x="2.5" y="9" width="3" height="7" fill="currentColor" opacity="0.7" />
      <rect x="7.5" y="6" width="3" height="10" fill="currentColor" opacity="0.7" />
      <rect x="12.5" y="11" width="3" height="5" fill="currentColor" opacity="0.7" />
      <polyline points="2,8 8,3 15,6" {...stroke} />
    </svg>
  ),
  image: (
    <svg {...S}>
      <rect x="2" y="3.5" width="14" height="11" rx="1.5" {...stroke} />
      <circle cx="6.2" cy="7.2" r="1.4" fill="currentColor" />
      <path d="M4 13 L8.5 9 L11 11.5 L13 9.8 L16 12.5" {...stroke} />
    </svg>
  ),
};

export function typeIcon(type: ElementType): JSX.Element {
  return ICONS[type];
}

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
