import { useCallback, useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';

import { errorMessage } from '@/lib/api';

interface ResourceOptions<T> {
  /** Recarga cada n ms (o según los datos: false = no recargar). */
  interval?: number | ((data: T | undefined) => number | false);
  /** Al cambiar, se vuelve a cargar. */
  deps?: unknown[];
  /** Sin aviso si falla (p. ej. un refresco en segundo plano). */
  silent?: boolean;
}

/**
 * Datos de la API: los carga al montar (y al cambiar deps), los refresca
 * cada interval y avisa con un toast si falla. reload() vuelve a pedirlos.
 */
export function useResource<T>(fetch: () => Promise<T>, opts: ResourceOptions<T> = {}) {
  const [data, setData] = useState<T>();
  const [loading, setLoading] = useState(true);
  const fetchRef = useRef(fetch);
  fetchRef.current = fetch;
  const silent = opts.silent ?? false;

  const reload = useCallback(async () => {
    try {
      setData(await fetchRef.current());
    } catch (e) {
      if (!silent) toast.error(errorMessage(e));
    } finally {
      setLoading(false);
    }
  }, [silent]);

  useEffect(() => void reload(), [reload, ...(opts.deps ?? [])]);

  const { interval } = opts;
  const every = typeof interval === 'function' ? interval(data) : interval;
  useEffect(() => {
    if (!every) return;
    const id = setInterval(reload, every);
    return () => clearInterval(id);
  }, [every, reload]);

  return { data, setData, loading, reload };
}

/**
 * Acciones sueltas (activar, borrar…): ejecuta fn, avisa del error con un
 * toast y, si sale bien, muestra success y llama a then.
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
