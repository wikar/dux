import { useEffect, useId, useRef } from "react";
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
  const titleId = useId();
  const modalRef = useRef<HTMLDivElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    const previousFocus = document.activeElement as HTMLElement | null;
    closeRef.current?.focus();
    function onKey(e: globalThis.KeyboardEvent) {
      if (e.key === "Escape") {
        onCloseRef.current();
        return;
      }
      if (e.key !== "Tab") return;
      const focusable = modalRef.current?.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
      );
      if (!focusable?.length) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      previousFocus?.focus();
    };
  }, []);

  return (
    <div className={styles.overlay} onMouseDown={onClose}>
      <div
        ref={modalRef}
        className={styles.modal}
        style={width ? { width } : undefined}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className={styles.header} id={titleId}>
          {title}
          <button ref={closeRef} className={styles.close} title="Close" aria-label="Close" onClick={onClose}>
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
