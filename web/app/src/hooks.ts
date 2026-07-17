import { useCallback, useEffect, useRef, useState } from "react";

/** Minimal async-resource hook (createResource equivalent): runs fetcher on
 *  mount and whenever deps change; refetch() re-runs it manually. Results from
 *  superseded fetches are discarded. */
export function useFetch<T>(fetcher: () => Promise<T>, deps: unknown[] = []) {
  const [data, setData] = useState<T | undefined>(undefined);
  const [error, setError] = useState<Error | null>(null);
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);
  const seq = useRef(0);

  useEffect(() => {
    const id = ++seq.current;
    setLoading(true);
    fetcher()
      .then((d) => {
        if (seq.current !== id) return;
        setData(d);
        setError(null);
      })
      .catch((e) => {
        if (seq.current !== id) return;
        setError(e instanceof Error ? e : new Error(String(e)));
      })
      .finally(() => {
        if (seq.current === id) setLoading(false);
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);

  const refetch = useCallback(() => setTick((t) => t + 1), []);
  return { data, error, loading, refetch };
}
