import { useCallback, useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';

import { errorMessage } from '@/lib/api';

/**
 * Data from the API: loaded on mount and refreshed every interval ms.
 * If the load fails with no previous data it returns error (the screen renders
 * it with ResourceState: "empty" is not the same as "could not load"); with
 * data already on screen it warns with a toast and keeps it. Background
 * refreshes fail silently. reload() asks for the data again.
 */
export function useResource<T>(fetch: () => Promise<T>, opts: { interval?: number } = {}) {
  const [data, setData] = useState<T>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const loaded = useRef(false);
  const fetchRef = useRef(fetch);
  fetchRef.current = fetch;

  const load = useCallback(async (quiet: boolean) => {
    try {
      setData(await fetchRef.current());
      loaded.current = true;
      setError(undefined);
    } catch (e) {
      if (!loaded.current) setError(errorMessage(e));
      else if (!quiet) toast.error(errorMessage(e));
    } finally {
      setLoading(false);
    }
  }, []);
  const reload = useCallback(() => load(false), [load]);

  useEffect(() => void reload(), [reload]);

  const { interval } = opts;
  useEffect(() => {
    if (!interval) return;
    const id = setInterval(() => load(true), interval);
    return () => clearInterval(id);
  }, [interval, load]);

  return { data, setData, loading, error, reload };
}

/**
 * One-off actions (enable, delete…): it runs fn, reports any error with a
 * toast and, when it works, shows success and calls then.
 */
export function useAction() {
  return useCallback(async (fn: () => Promise<unknown>, opts: { success?: string; then?: () => unknown } = {}) => {
    try {
      await fn();
      if (opts.success) toast.success(opts.success);
      await opts.then?.();
      return true;
    } catch (e) {
      toast.error(errorMessage(e));
      return false;
    }
  }, []);
}
