import Modal, { modalStyles } from "../../components/Modal";
import { overwriteConflict, reloadConflict } from "../actions";
import { useUiStore } from "../store";

/** Shown when a save hits a 409/428: the file on disk changed since it was
 *  loaded (another user, an agent, or a git pull). */
export default function ConflictDialog() {
  const conflict = useUiStore((s) => s.conflict)!;
  const setConflict = useUiStore((s) => s.setConflict);

  const when = conflict.modified ? new Date(conflict.modified).toLocaleString() : "unknown time";

  return (
    <Modal
      title="Save conflict"
      onClose={() => setConflict(null)}
      footer={
        <>
          <button className={modalStyles.btn} onClick={() => setConflict(null)}>
            Cancel
          </button>
          <button className={modalStyles.btn} onClick={() => void reloadConflict()}>
            Reload from disk
          </button>
          <button className={modalStyles.btnDanger} onClick={() => void overwriteConflict()}>
            Overwrite
          </button>
        </>
      }
    >
      <p className={modalStyles.hint}>
        {conflict.message}
        <br />
        The file on disk changed (last modified {when}) since this dashboard was loaded. Overwrite
        it with your version, or discard your changes and reload the disk version.
      </p>
    </Modal>
  );
}
