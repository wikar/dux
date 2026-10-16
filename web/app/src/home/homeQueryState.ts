import { generateQuery, isMetricField, isNumeric } from "@dux/core";
import type { Aggregate, DragPayload, DropField, FilterField, FilterOp } from "@dux/core";

export interface HomeQueryState {
  fields: DropField[];
  filters: FilterField[];
  generatedQuery: string;
  activeQuery: string;
  committedQuery: string;
  isDirty: boolean;
  runId: number;
}

export type HomeQueryAction =
  | { type: "add-field"; payload: DragPayload }
  | { type: "remove-field"; index: number }
  | { type: "set-aggregate"; index: number; aggregate: Aggregate }
  | { type: "reorder-fields"; from: number; to: number }
  | { type: "add-filter"; payload: DragPayload }
  | { type: "remove-filter"; index: number }
  | { type: "set-filter-value"; index: number; value: string }
  | { type: "set-filter-op"; index: number; op: FilterOp }
  | { type: "reorder-filters"; from: number; to: number }
  | { type: "edit-query"; value: string }
  | { type: "run-query" };

export const initialHomeQueryState: HomeQueryState = {
  fields: [],
  filters: [],
  generatedQuery: "",
  activeQuery: "",
  committedQuery: "",
  isDirty: false,
  runId: 0,
};

function withBuilder(state: HomeQueryState, fields: DropField[], filters: FilterField[]): HomeQueryState {
  const generatedQuery = generateQuery(fields, filters);
  if (generatedQuery === state.generatedQuery) return { ...state, fields, filters };
  return {
    ...state,
    fields,
    filters,
    generatedQuery,
    activeQuery: generatedQuery,
    committedQuery: generatedQuery,
    isDirty: false,
    runId: state.runId + 1,
  };
}

export function homeQueryReducer(state: HomeQueryState, action: HomeQueryAction): HomeQueryState {
  switch (action.type) {
    case "add-field": {
      const { payload } = action;
      if (state.fields.some((f) => f.table === payload.table && f.name === payload.name)) return state;
      const field: DropField = {
        table: payload.table,
        name: payload.name,
        kind: payload.kind,
        dataType: payload.dataType,
        aggregate: payload.kind === "column" && isNumeric(payload.dataType) ? "SUM" : undefined,
      };
      if (isMetricField(field)) return withBuilder(state, [...state.fields, field], state.filters);
      const insertAt = state.fields.findIndex(isMetricField);
      const fields = [...state.fields];
      fields.splice(insertAt < 0 ? fields.length : insertAt, 0, field);
      return withBuilder(state, fields, state.filters);
    }
    case "remove-field":
      if (!state.fields[action.index]) return state;
      return withBuilder(state, state.fields.filter((_, i) => i !== action.index), state.filters);
    case "set-aggregate": {
      const previous = state.fields[action.index];
      if (!previous || previous.aggregate === action.aggregate) return state;
      const fields = [...state.fields];
      const updated = { ...previous, aggregate: action.aggregate };
      const wasMetric = isMetricField(previous), isMetric = isMetricField(updated);
      fields[action.index] = updated;
      if (wasMetric !== isMetric) {
        const [item] = fields.splice(action.index, 1);
        if (isMetric) fields.push(item);
        else {
          const insertAt = fields.findIndex(isMetricField);
          fields.splice(insertAt < 0 ? fields.length : insertAt, 0, item);
        }
      }
      return withBuilder(state, fields, state.filters);
    }
    case "reorder-fields": {
      const { from, to } = action;
      const item = state.fields[from];
      if (!item || to < 0 || to >= state.fields.length) return state;
      const nonMetricCount = state.fields.filter((field, i) => i !== from && !isMetricField(field)).length;
      const target = isMetricField(item) ? Math.max(to, nonMetricCount) : Math.min(to, nonMetricCount);
      if (target === from) return state;
      const fields = [...state.fields];
      const [moved] = fields.splice(from, 1);
      fields.splice(target, 0, moved);
      return withBuilder(state, fields, state.filters);
    }
    case "add-filter": {
      const { payload } = action;
      if (state.filters.some((f) => f.table === payload.table && f.name === payload.name)) return state;
      const filter: FilterField = { table: payload.table, name: payload.name, dataType: payload.dataType, op: "=", value: "" };
      return withBuilder(state, state.fields, [...state.filters, filter]);
    }
    case "remove-filter":
      if (!state.filters[action.index]) return state;
      return withBuilder(state, state.fields, state.filters.filter((_, i) => i !== action.index));
    case "set-filter-value": {
      const current = state.filters[action.index];
      if (!current || current.value === action.value) return state;
      return withBuilder(state, state.fields, state.filters.map((f, i) => i === action.index ? { ...f, value: action.value } : f));
    }
    case "set-filter-op": {
      const current = state.filters[action.index];
      if (!current || current.op === action.op) return state;
      return withBuilder(state, state.fields, state.filters.map((f, i) => i === action.index ? { ...f, op: action.op } : f));
    }
    case "reorder-filters": {
      const { from, to } = action;
      if (from < 0 || from >= state.filters.length || to < 0 || to >= state.filters.length || from === to) return state;
      const filters = [...state.filters];
      const [moved] = filters.splice(from, 1);
      filters.splice(to, 0, moved);
      return withBuilder(state, state.fields, filters);
    }
    case "edit-query":
      return { ...state, activeQuery: action.value, isDirty: true };
    case "run-query":
      return { ...state, committedQuery: state.activeQuery, isDirty: false, runId: state.runId + 1 };
  }
}
