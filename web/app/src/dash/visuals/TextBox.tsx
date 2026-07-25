// Text: static markdown, authored in the settings pane.
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import styles from "../components/ElementBody.module.css";
import { S } from "../glyphs";
import type { StaticBodyProps, VisualDef } from "./types";

function TextBody({ el }: StaticBodyProps) {
  return (
    <div className={styles.markdown}>
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{el.text?.markdown ?? ""}</ReactMarkdown>
    </div>
  );
}

const textBox: VisualDef = {
  type: "text",
  label: "Text",
  icon: (
    <svg {...S}>
      <text x="9" y="13.5" textAnchor="middle" fontSize="13" fontWeight="bold" fill="currentColor">
        T
      </text>
    </svg>
  ),
  size: { w: 280, h: 160 },
  // The markdown carries its own heading; a title bar would double it up.
  titled: false,
  seed: () => ({ text: { markdown: "## Text\n\nEdit the markdown in the settings pane." } }),
  Body: TextBody,
};

export default textBox;
