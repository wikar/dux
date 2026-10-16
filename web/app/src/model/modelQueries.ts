import type { QueryClient } from "@tanstack/react-query";
import { duxClient } from "@dux/core";

export const schemaQueryKey = ["schema"] as const;
export const measuresQueryKey = ["measures"] as const;

export const schemaQuery = {
  queryKey: schemaQueryKey,
  queryFn: () => duxClient.fetchSchema(),
};

export const measuresQuery = {
  queryKey: measuresQueryKey,
  queryFn: () => duxClient.fetchMeasures(),
};

/** Invalidate the shared semantic-model snapshots affected by a mutation. */
export function invalidateModelQueries(
  queryClient: QueryClient,
  affected: { schema?: boolean; measures?: boolean } = { schema: true, measures: true },
) {
  const invalidations = [];
  if (affected.schema !== false) invalidations.push(queryClient.invalidateQueries({ queryKey: schemaQueryKey }));
  if (affected.measures !== false) invalidations.push(queryClient.invalidateQueries({ queryKey: measuresQueryKey }));
  return Promise.all(invalidations);
}

export async function refreshModel(queryClient: QueryClient) {
  await duxClient.refresh();
  await invalidateModelQueries(queryClient);
}
