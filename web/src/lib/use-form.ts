import { useCallback, useState, type FormEvent } from 'react';

import { errorMessage } from '@/lib/api';

/** Estado de un formulario con un setter por campo. */
export function useForm<T extends object>(initial: T | (() => T)) {
  const [form, setForm] = useState<T>(initial);
  const set = useCallback(<K extends keyof T>(key: K, value: T[K]) => setForm((f) => ({ ...f, [key]: value })), []);
  return { form, set, setForm };
}

/**
 * Envío de un formulario: evita el submit del navegador, marca busy mientras
 * dura y deja en error el mensaje traducido si falla.
 */
export function useSubmit(fn: () => Promise<unknown>) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  async function submit(e?: FormEvent) {
    e?.preventDefault();
    setError('');
    setBusy(true);
    try {
      await fn();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }
  return { submit, busy, error, setError };
}
