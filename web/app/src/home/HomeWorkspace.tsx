import { useReducer, useRef, useState } from "react";
import type { QueryFailedError } from "@dux/core";
import SchemaTree from "../components/SchemaTree";
import DropZone from "../components/DropZone";
import QueryPreview from "../components/QueryPreview";
import ResultTable from "../components/ResultTable";
import styles from "../App.module.css";
import { homeQueryReducer, initialHomeQueryState } from "./homeQueryState";

/** Home query building and result state live together and stay mounted while
 *  another workspace is visible so a tab switch preserves the current work. */
export default function HomeWorkspace({ active, showHidden }: { active: boolean; showHidden: boolean }) {
  const [includeEmpty, setIncludeEmpty] = useState(false);
  const [queryError, setQueryError] = useState<{ error: QueryFailedError; query: string } | null>(null);
  const [queryState, dispatch] = useReducer(homeQueryReducer, initialHomeQueryState);

  function handleQueryChange(q: string) {
    dispatch({ type: "edit-query", value: q });
    setQueryError(null);
  }

  function commitQuery() {
    dispatch({ type: "run-query" });
  }

  return (
    <div className={styles.layout} style={!active ? { display: "none" } : undefined}>
      <div className={styles.col1}>
        <SchemaTree showHidden={showHidden} />
      </div>
      <div className={styles.col2}>
        <DropZone
          label="Columns / Measures"
          id="fields"
          items={queryState.fields}
          onDrop={(payload) => dispatch({ type: "add-field", payload })}
          onRemove={(index) => dispatch({ type: "remove-field", index })}
          onAggChange={(index, aggregate) => {
            if (aggregate) dispatch({ type: "set-aggregate", index, aggregate });
          }}
          onReorder={(from, to) => dispatch({ type: "reorder-fields", from, to })}
        />
        <DropZone
          label="Filters"
          id="filters"
          items={queryState.filters}
          onDrop={(payload) => dispatch({ type: "add-filter", payload })}
          onRemove={(index) => dispatch({ type: "remove-filter", index })}
          onValueChange={(index, value) => dispatch({ type: "set-filter-value", index, value })}
          onOpChange={(index, op) => dispatch({ type: "set-filter-op", index, op })}
          onReorder={(from, to) => dispatch({ type: "reorder-filters", from, to })}
        />
      </div>
      <div className={styles.col3}>
        <ResultTable
          query={queryState.committedQuery}
          runId={queryState.runId}
          includeEmpty={includeEmpty}
          onQueryError={(err, executedQuery) => setQueryError(
            err && executedQuery === queryState.activeQuery ? { error: err, query: executedQuery } : null
          )}
        >
          {(copyResults) => (
            <QueryPreview
              query={queryState.activeQuery}
              isDirty={queryState.isDirty}
              onQueryChange={handleQueryChange}
              onRun={commitQuery}
              includeEmpty={includeEmpty}
              onToggleIncludeEmpty={() => setIncludeEmpty((v) => !v)}
              onCopyResults={copyResults}
              error={queryError?.query === queryState.activeQuery ? queryError.error : null}
            />
          )}
        </ResultTable>
      </div>
    </div>
  );
}
