import { useState } from 'react';
import { X } from 'lucide-react';

interface Props {
  id?: string;
  value: string[];
  onChange: (domains: string[]) => void;
}

/**
 * Campo de dominios con "chips". El texto pendiente se convierte en dominio al
 * pulsar Enter, coma o espacio, y al perder el foco (por ejemplo, al pulsar
 * Guardar sin haber pulsado Enter).
 */
export function DomainsInput({ id, value, onChange }: Props) {
  const [draft, setDraft] = useState('');

  function commit() {
    const next = [...value];
    for (const raw of draft.split(/[\s,]+/)) {
      const d = raw.trim().toLowerCase();
      if (d && !next.includes(d)) next.push(d);
    }
    if (next.length !== value.length) onChange(next);
    setDraft('');
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter' || e.key === ',' || e.key === ' ') {
      e.preventDefault();
      commit();
    } else if (e.key === 'Backspace' && !draft && value.length) {
      onChange(value.slice(0, -1));
    }
  }

  return (
    <div className="space-y-2">
      <div className="flex min-h-9 flex-wrap items-center gap-1.5 rounded-md border border-input px-2 py-1.5 focus-within:ring-[3px] focus-within:ring-ring/50">
        {value.map((d) => (
          <span key={d} className="inline-flex items-center gap-1 rounded bg-secondary py-0.5 pr-1 pl-2 text-sm font-medium">
            {d}
            <button
              type="button"
              className="rounded p-0.5 text-muted-foreground hover:text-foreground"
              onClick={() => onChange(value.filter((x) => x !== d))}
              aria-label={`Quitar ${d}`}
            >
              <X className="size-3" />
            </button>
          </span>
        ))}
        <input
          id={id}
          className="min-w-40 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
          placeholder={value.length ? '' : 'app.midominio.com'}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={onKeyDown}
          onBlur={commit}
        />
      </div>
      <p className="text-xs text-muted-foreground">Pulsa Enter o coma para añadir varios.</p>
    </div>
  );
}
