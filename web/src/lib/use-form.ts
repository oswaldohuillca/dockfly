import { useCallback, useState, type FormEvent } from 'react';

import { errorMessage } from '@/lib/api';

/** A form's state, with one setter per field. */
export function useForm<T extends object>(initial: T | (() => T)) {
  const [form, setForm] = useState<T>(initial);
  const set = useCallback(<K extends keyof T>(key: K, value: T[K]) => setForm((f) => ({ ...f, [key]: value })), []);
  return { form, set, setForm };
}

/**
 * Form submission: it prevents the browser's own submit, marks busy while it
 * runs and leaves the translated message in error if it fails.
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
