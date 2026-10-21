import { useEffect, useState } from 'react';
import { Link } from '@tanstack/react-router';
import { Trans } from '@lingui/react/macro';

import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { api, type AccessList } from '@/lib/api';

/** Elegir quién puede acceder: público o una lista de acceso. */
export function AccessSelect({ value, onChange }: { value: number | null; onChange: (id: number | null) => void }) {
  const [lists, setLists] = useState<AccessList[]>([]);
  useEffect(() => {
    api.listAccessLists().then(setLists).catch(() => {});
  }, []);
  return (
    <div className="space-y-2">
      <Label>
        <Trans>Access</Trans>
      </Label>
      <Select value={value ? String(value) : 'public'} onValueChange={(v) => onChange(v === 'public' ? null : Number(v))}>
        <SelectTrigger className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="public">
            <Trans>Public</Trans>
          </SelectItem>
          {lists.map((l) => (
            <SelectItem key={l.id} value={String(l.id)}>
              {l.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <p className="text-xs text-muted-foreground">
        <Trans>
          Protect it with a username and password, or restrict it to certain IPs. Lists are created in{' '}
          <Link to="/settings" className="underline">
            Settings
          </Link>
          .
        </Trans>
      </p>
    </div>
  );
}
