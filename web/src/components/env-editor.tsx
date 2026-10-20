import { Plus, X } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';

export type EnvRow = { key: string; value: string };

export const envToRows = (env: Record<string, string>): EnvRow[] =>
  Object.entries(env).map(([key, value]) => ({ key, value }));

export const rowsToEnv = (rows: EnvRow[]): Record<string, string> =>
  Object.fromEntries(rows.filter((r) => r.key.trim()).map((r) => [r.key.trim(), r.value]));

/** Editor de variables de entorno (NOMBRE = valor). */
export function EnvEditor({ rows, onChange }: { rows: EnvRow[]; onChange: (rows: EnvRow[]) => void }) {
  return (
    <div className="space-y-2">
      {rows.map((row, i) => (
        <div key={i} className="flex gap-2">
          <Input
            className="font-mono"
            placeholder="NOMBRE"
            value={row.key}
            onChange={(e) =>
              onChange(rows.map((r, j) => (j === i ? { ...r, key: e.target.value.replace(/[^A-Za-z0-9_]/g, '_') } : r)))
            }
          />
          <Input
            className="font-mono"
            placeholder="valor"
            value={row.value}
            onChange={(e) => onChange(rows.map((r, j) => (j === i ? { ...r, value: e.target.value } : r)))}
          />
          <Button type="button" variant="ghost" size="icon" onClick={() => onChange(rows.filter((_, j) => j !== i))}>
            <X />
          </Button>
        </div>
      ))}
      <Button type="button" variant="outline" size="sm" onClick={() => onChange([...rows, { key: '', value: '' }])}>
        <Plus /> Añadir variable
      </Button>
    </div>
  );
}
