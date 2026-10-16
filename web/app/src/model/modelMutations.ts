import type { QueryClient } from "@tanstack/react-query";
import type { MeasureFormat } from "@dux/core";
import { duxClient } from "@dux/core";
import { invalidateModelQueries } from "./modelQueries";

export type MeasureDraft = {
  table: string;
  name: string;
  expression: string;
  format?: MeasureFormat;
};

export async function saveModelMeasure(
  queryClient: QueryClient,
  measure: MeasureDraft,
  original?: Pick<MeasureDraft, "table" | "name">,
) {
  await duxClient.addMeasure(measure.table, measure.name, measure.expression, measure.format);
  if (original && (original.table !== measure.table || original.name !== measure.name)) {
    await duxClient.deleteMeasure(original.table, original.name);
  }
  await invalidateModelQueries(queryClient);
}

export async function deleteModelMeasure(queryClient: QueryClient, table: string, name: string) {
  await duxClient.deleteMeasure(table, name);
  await invalidateModelQueries(queryClient);
}
