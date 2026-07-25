// Image: an external URL or an uploaded /api/dash/assets/ path.
import { imageUrl } from "../api";
import styles from "../components/ElementBody.module.css";
import { S, stroke } from "../glyphs";
import type { StaticBodyProps, VisualDef } from "./types";

const icon = (
  <svg {...S}>
    <rect x="2" y="3.5" width="14" height="11" rx="1.5" {...stroke} />
    <circle cx="6.2" cy="7.2" r="1.4" fill="currentColor" />
    <path d="M4 13 L8.5 9 L11 11.5 L13 9.8 L16 12.5" {...stroke} />
  </svg>
);

function ImageBody({ el }: StaticBodyProps) {
  const url = el.image?.url?.trim();
  if (!url) {
    return (
      <div className={styles.placeholder}>
        <span className={styles.icon}>{icon}</span>
        <span className={styles.hint}>Set an image URL in the settings pane</span>
      </div>
    );
  }
  return (
    <img
      className={styles.image}
      src={imageUrl(url)}
      alt={el.title?.text ?? ""}
      style={{ objectFit: el.image?.fit ?? "contain" }}
      draggable={false}
    />
  );
}

const imageBox: VisualDef = {
  type: "image",
  label: "Image",
  icon,
  size: { w: 200, h: 120 },
  // The picture is the content; a title bar would crop it.
  titled: false,
  seed: () => ({ image: { fit: "contain" } }),
  Body: ImageBody,
};

export default imageBox;
