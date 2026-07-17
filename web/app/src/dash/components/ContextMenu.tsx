import { useEffect } from "react";
import styles from "./ContextMenu.module.css";
import { bringToFront, duplicateElement, removeElement, sendToBack } from "../docOps";

export interface MenuState {
  x: number;
  y: number;
  id: string;
}

export default function ContextMenu({ menu, onClose }: { menu: MenuState; onClose: () => void }) {
  useEffect(() => {
    const close = () => onClose();
    window.addEventListener("pointerdown", close);
    window.addEventListener("blur", close);
    return () => {
      window.removeEventListener("pointerdown", close);
      window.removeEventListener("blur", close);
    };
  }, [onClose]);

  const run = (fn: () => void) => () => {
    fn();
    onClose();
  };

  return (
    <div
      className={styles.menu}
      style={{ left: menu.x, top: menu.y }}
      onPointerDown={(e) => e.stopPropagation()}
    >
      <button onClick={run(() => duplicateElement(menu.id))}>Duplicate</button>
      <button onClick={run(() => bringToFront(menu.id))}>Bring to front</button>
      <button onClick={run(() => sendToBack(menu.id))}>Send to back</button>
      <div className={styles.divider} />
      <button className={styles.danger} onClick={run(() => removeElement(menu.id))}>
        Delete
      </button>
    </div>
  );
}
