import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import styles from "./ElementsPane.module.css";
import ColorPicker from "./ColorPicker";
import { normalizeColor } from "./color";
import Modal, { modalStyles } from "../../components/Modal";
import { putTheme } from "../api";
import { DEFAULT_THEME, useGlobalResolvedTheme, useGlobalTheme } from "../data";
import { useDocStore } from "../store";
import type { ThemeTokens } from "../types";
import { displayMessage } from "../message";

// ─── Theme (all visual tokens; global template ← per-dashboard overrides) ────

const COLOR_TOKENS: { key: keyof ThemeTokens; label: string }[] = [
  { key: "background", label: "Background color" },
  { key: "elementBackground", label: "Element background" },
  { key: "titleBackground", label: "Title background" },
  { key: "border", label: "Border color" },
  { key: "text", label: "Text color" },
];

const STATUS_COLOR_TOKENS = [
  { key: "positive", label: "Positive" },
  { key: "negative", label: "Negative" },
  { key: "neutral", label: "Neutral" },
  { key: "error", label: "Error" },
  { key: "warning", label: "Warning" },
] as const;

/** One themable color: the Sketch picker (hex field and alpha slider live
 *  inside it) plus a text field that also accepts any CSS color and, left
 *  empty, falls back to the inherited value. */
function ColorField({
  value,
  placeholder,
  onCommit,
}: {
  value: string;
  placeholder: string;
  onCommit: (v: string) => void;
}) {
  const shown = value || placeholder;
  return (
    <div className={styles.row}>
      <ColorPicker
        className={styles.color}
        value={shown}
        title={value || `inherited: ${placeholder}`}
        onChange={onCommit}
      />
      <input
        className={styles.input}
        key={`t:${value}`}
        defaultValue={value}
        placeholder={placeholder}
        onBlur={(e) => onCommit(normalizeColor(e.target.value.trim()))}
      />
    </div>
  );
}

interface ThemeEditorProps {
  /** Sparse tokens being edited (empty/absent = inherit). */
  tokens: ThemeTokens;
  /** Effective values one level up — shown as placeholders/previews. */
  inherited: Required<ThemeTokens>;
  onToken: (key: keyof ThemeTokens, value: string | string[] | undefined) => void;
}

/** Shared token editor for both the global theme and dashboard overrides. */
function ThemeEditor({ tokens, inherited, onToken }: ThemeEditorProps) {
  return (
    <>
      {COLOR_TOKENS.map((t) => (
        <div key={t.key}>
          <label className={styles.label}>{t.label}</label>
          <ColorField
            value={(tokens[t.key] as string | undefined) ?? ""}
            placeholder={inherited[t.key] as string}
            onCommit={(v) => onToken(t.key, v || undefined)}
          />
        </div>
      ))}

      <label className={styles.label}>Semantic colors (positive → warning)</label>
      <div className={styles.swatchRow}>
        {STATUS_COLOR_TOKENS.map(({ key, label }) => (
          <ColorPicker
            key={key}
            className={styles.swatchInput}
            value={tokens[key] ?? inherited[key]}
            title={`${label}: ${tokens[key] ?? `inherited ${inherited[key]}`}`}
            onChange={(v) => onToken(key, v)}
          />
        ))}
      </div>
      {STATUS_COLOR_TOKENS.some(({ key }) => tokens[key]) && (
        <button className={styles.btn} onClick={() => STATUS_COLOR_TOKENS.forEach(({ key }) => onToken(key, undefined))}>
          Reset semantic colors
        </button>
      )}

      <label className={styles.label}>Background image URL</label>
      <input
        className={styles.input}
        key={`bgi:${tokens.backgroundImage ?? ""}`}
        defaultValue={tokens.backgroundImage ?? ""}
        placeholder={inherited.backgroundImage || "none"}
        onBlur={(e) => onToken("backgroundImage", e.target.value.trim() || undefined)}
      />

      <label className={styles.label}>Background image fit</label>
      <select
        className={styles.input}
        value={tokens.backgroundFit ?? ""}
        onChange={(e) => onToken("backgroundFit", e.target.value || undefined)}
      >
        <option value="">inherit ({inherited.backgroundFit})</option>
        <option value="cover">cover</option>
        <option value="contain">contain</option>
        <option value="fill">fill</option>
        <option value="tile">tile</option>
      </select>

      <label className={styles.label}>Font family</label>
      <input
        className={styles.input}
        key={`ff:${tokens.fontFamily ?? ""}`}
        defaultValue={tokens.fontFamily ?? ""}
        placeholder={inherited.fontFamily}
        onBlur={(e) => onToken("fontFamily", e.target.value.trim() || undefined)}
      />

      <label className={styles.label}>Data colors (left → right)</label>
      <PaletteEditor
        colors={tokens.palette ?? inherited.palette}
        onChange={(p) => onToken("palette", p.length ? p : undefined)}
      />
      {tokens.palette && (
        <button className={styles.btn} onClick={() => onToken("palette", undefined)}>
          Reset to inherited
        </button>
      )}
    </>
  );
}

export default function ThemeSection() {
  const doc = useDocStore((s) => s.doc)!;
  const inherited = useGlobalResolvedTheme();
  const [globalOpen, setGlobalOpen] = useState(false);
  const tokens = (doc.theme ?? {}) as ThemeTokens;

  const onToken = (key: keyof ThemeTokens, value: string | string[] | undefined) =>
    useDocStore.getState().update((d) => {
      const theme = { ...(d.theme ?? {}) } as Record<string, unknown>;
      if (value === undefined) delete theme[key];
      else theme[key] = value;
      const next = { ...d };
      if (Object.keys(theme).length > 0) next.theme = theme;
      else delete next.theme;
      // The theme tokens own the background now — writing one clears the
      // legacy canvas.background field it supersedes.
      if (key === "background" || key === "backgroundImage" || key === "backgroundFit") {
        const bg = { ...next.canvas.background };
        if (key === "background") delete bg.color;
        if (key === "backgroundImage") {
          delete bg.url;
          delete bg.asset;
        }
        if (key === "backgroundFit") delete bg.fit;
        next.canvas = { ...next.canvas };
        if (Object.keys(bg).length > 0) next.canvas.background = bg;
        else delete next.canvas.background;
      }
      return next;
    });

  return (
    <div className={styles.section}>
      <div className={styles.heading}>
        Theme <span className={styles.id}>overrides the global theme</span>
      </div>
      <ThemeEditor tokens={tokens} inherited={inherited} onToken={onToken} />
      <button className={styles.btn} onClick={() => setGlobalOpen(true)}>
        Edit global theme…
      </button>
      {globalOpen && <GlobalThemeModal onClose={() => setGlobalOpen(false)} />}
    </div>
  );
}

/** Series colors: a compact, wrapping swatch grid. */
function PaletteEditor({ colors, onChange }: { colors: string[]; onChange: (c: string[]) => void }) {
  return (
    <div className={styles.swatchRow}>
      {colors.map((c, i) => (
        <span key={i} className={styles.swatchEdit}>
          <ColorPicker
            className={styles.swatchInput}
            value={c}
            onChange={(v) => onChange(colors.map((x, j) => (j === i ? v : x)))}
          />
          <button
            className={styles.swatchRemove}
            title="Remove color"
            onClick={() => onChange(colors.filter((_, j) => j !== i))}
          >
            ×
          </button>
        </span>
      ))}
      <button className={styles.btn} onClick={() => onChange([...colors, "#89b4fa"])} title="Add color">
        +
      </button>
    </div>
  );
}

function GlobalThemeModal({ onClose }: { onClose: () => void }) {
  const { data } = useGlobalTheme();
  const queryClient = useQueryClient();
  const [tokens, setTokens] = useState<Record<string, unknown>>(() => ({ ...(data?.tokens ?? {}) }));
  const [error, setError] = useState<string | null>(null);

  const onToken = (key: keyof ThemeTokens, value: string | string[] | undefined) =>
    setTokens((t) => {
      const next = { ...t };
      if (value === undefined) delete next[key];
      else next[key] = value;
      return next;
    });

  const save = async () => {
    try {
      await putTheme(tokens, data?.etag ?? null);
      await queryClient.invalidateQueries({ queryKey: ["dash-theme"] });
      onClose();
    } catch (e) {
      setError(displayMessage(e));
    }
  };

  return (
    <Modal
      title="Global theme"
      onClose={onClose}
      footer={
        <>
          <button className={modalStyles.btn} onClick={onClose}>
            Cancel
          </button>
          <button className={modalStyles.btnPrimary} onClick={() => void save()}>
            Save
          </button>
        </>
      }
    >
      <p className={modalStyles.hint}>
        Saved to <code>dashboards/theme.json</code> — the template every dashboard inherits
        (each dashboard can override single tokens). Also exportable/importable via{" "}
        <code>GET/PUT /api/dash/theme</code>.
      </p>
      <div className={styles.themeModalBody}>
        <ThemeEditor tokens={tokens as ThemeTokens} inherited={DEFAULT_THEME} onToken={onToken} />
      </div>
      {error && <div className={modalStyles.error}>{error}</div>}
    </Modal>
  );
}
