import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import styles from "./ElementBody.module.css";
import { TYPE_LABEL } from "../docOps";
import type { DashElement } from "../types";
import { typeIcon } from "./typeIcons";

/** Per-type element body. M3 renders text/markdown; data-backed types show a
 *  placeholder until their renderers land (M4–M6). */
export default function ElementBody({ el }: { el: DashElement }) {
  if (el.type === "text") {
    return (
      <div className={styles.markdown}>
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{el.text?.markdown ?? ""}</ReactMarkdown>
      </div>
    );
  }
  return (
    <div className={styles.placeholder}>
      <span className={styles.icon}>{typeIcon(el.type)}</span>
      <span>{TYPE_LABEL[el.type]}</span>
      <span className={styles.hint}>arrives in a later milestone</span>
    </div>
  );
}
