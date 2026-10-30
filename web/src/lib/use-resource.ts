import { useCallback, useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';

import { errorMessage } from '@/lib/api';

/**
 * Datos de la API: los carga al montar, los refresca cada interval ms y avisa
 * con un toast si falla la carga (los refrescos en segundo plano fallan en
 * silencio, para no repetir el aviso en cada tick). reload() vuelve a pedirlos.
 */
export function useResource<T>(fetch: () => Promise<T>, opts: { interval?: number } = {}) {
  const [data, setData] = useState<T>();
  const [loading, setLoading] = useState(true);
  const fetchRef = useRef(fetch);
  fetchRef.current = fetch;

  const load = useCallback(async (quiet: boolean) => {
    try {
      setData(await fetchRef.current());
    } catch (e) {
      if (!quiet) toast.error(errorMessage(e));
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
