export const TEXT = "var(--th-text)";
// Opaque, unlike the frosted element background: a tooltip sits over the marks
// it describes and has to stay legible against them.
export const TOOLTIP_STYLE = {
  backgroundColor: "var(--th-bg)",
  border: "1px solid var(--th-border)",
  borderRadius: 6,
  fontSize: 12,
  color: TEXT,
} as const;
export const TOOLTIP_LABEL_STYLE = { color: TEXT } as const;
export const LEGEND_STYLE = { fontSize: 11, color: TEXT } as const;
