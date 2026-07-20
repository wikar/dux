import { useEffect } from "react";
import type { ReactNode } from "react";
import styles from "./Modal.module.css";

interface Props {
  title: string;
  onClose: () => void;
  footer?: ReactNode;
  /** Override the default modal box width (e.g. wider preview tables). */
  width?: number | string;
  children: ReactNode;
}

/** Modal shell in the builder's modal idiom (bordered header/body/footer).
 *  Buttons: use styles.btn / styles.btnPrimary / styles.btnDanger from
 *  Modal.module.css in the footer. Escape closes it. */
export default function Modal({ title, onClose, footer, width, children }: Props) {
  useEffect(() => {
    function onKey(e: globalThis.KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className={styles.overlay} onMouseDown={onClose}>
      <div className={styles.modal} style={width ? { width } : undefined} onMouseDown={(e) => e.stopPropagation()}>
        <div className={styles.header}>
          {title}
          <button className={styles.close} title="Close" onClick={onClose}>
            ✕
          </button>
        </div>
        <div className={styles.body}>{children}</div>
        {footer && <div className={styles.footer}>{footer}</div>}
      </div>
    </div>
  );
}

export { styles as modalStyles };
