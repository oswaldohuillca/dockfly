import { useCallback, useEffect, useState } from 'react';
import { Loader2, Pencil, Plus, ShieldCheck, Trash2, TriangleAlert, X } from 'lucide-react';
import { toast } from 'sonner';
import { Plural, Trans, useLingui } from '@lingui/react/macro';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api, errorMessage, type AccessList } from '@/lib/api';

export function AccessLists() {
  const { t } = useLingui();
  const [lists, setLists] = useState<AccessList[]>([]);
  const [editing, setEditing] = useState<AccessList | 'new' | null>(null);
  const load = useCallback(() => {
    api.listAccessLists().then(setLists).catch((e) => toast.error(errorMessage(e)));
  }, []);
  useEffect(load, [load]);

  async function remove(l: AccessList) {
    try {
      await api.deleteAccessList(l.id);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">
          <Trans>Access lists</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>
            Protect proxy hosts, sites and apps with a username and password, or allow only certain IPs. If a list has
            both, both are required.
          </Trans>
        </CardDescription>
        <CardAction>
          <Button size="sm" onClick={() => setEditing('new')}>
            <Plus /> <Trans>New list</Trans>
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        {lists.length === 0 ? (
          <p className="py-4 text-center text-sm text-muted-foreground">
            <Trans>No access lists yet.</Trans>
          </p>
        ) : (
          <ul className="divide-y rounded-lg border">
            {lists.map((l) => (
              <li key={l.id} className="flex flex-wrap items-center gap-3 px-4 py-3">
                <ShieldCheck className="size-4 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="font-medium">{l.name}</div>
                  <div className="mt-1 flex flex-wrap gap-1">
                    {l.users.map((u) => (
                      <Badge key={u} variant="secondary" className="font-normal">
                        👤 {u}
                      </Badge>
                    ))}
                    {l.allow_ips.map((ip) => (
                      <Badge key={ip} variant="outline" className="font-mono font-normal">
                        {ip}
                      </Badge>
                    ))}
                  </div>
                </div>
                <span className="text-xs text-muted-foreground">
                  {l.used_by ? (
                    <Plural value={l.used_by} one="Protects # item" other="Protects # items" />
                  ) : (
                    <Trans>Unused</Trans>
                  )}
                </span>
                <Button variant="ghost" size="icon-sm" onClick={() => setEditing(l)} aria-label={t`Edit`}>
                  <Pencil />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-destructive hover:text-destructive"
                  onClick={() => remove(l)}
                  aria-label={t`Delete`}
                >
                  <Trash2 />
                </Button>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
      {editing && (
        <AccessListDialog
          list={editing === 'new' ? undefined : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            load();
          }}
        />
      )}
    </Card>
  );
}

function AccessListDialog({ list, onClose, onSaved }: { list?: AccessList; onClose: () => void; onSaved: () => void }) {
  const { t } = useLingui();
  const [name, setName] = useState(list?.name ?? '');
  const [users, setUsers] = useState(list?.users.map((u) => ({ username: u, password: '', existing: true })) ?? []);
  const [ips, setIps] = useState(list?.allow_ips.join('\n') ?? '');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    const input = {
      name,
      users: users.map(({ username, password }) => ({ username, password })),
      allow_ips: ips.split(/[\s,]+/).filter(Boolean),
    };
    try {
      if (list) await api.updateAccessList(list.id, input);
      else await api.createAccessList(input);
      toast.success(t`List saved`);
      onSaved();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={save} className="contents">
          <DialogHeader>
            <DialogTitle>{list ? <Trans>Edit {list.name}</Trans> : <Trans>New access list</Trans>}</DialogTitle>
            <DialogDescription>
              <Trans>Passwords are stored with bcrypt and are never shown again.</Trans>
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="acl-name">
              <Trans>Name</Trans>
            </Label>
            <Input id="acl-name" value={name} onChange={(e) => setName(e.target.value)} placeholder={t`Team`} required />
          </div>
          <div className="space-y-2">
            <Label>
              <Trans>Users</Trans>
            </Label>
            {users.map((u, i) => (
              <div key={i} className="flex gap-2">
                <Input
                  placeholder={t`username`}
                  value={u.username}
                  onChange={(e) => setUsers(users.map((x, j) => (j === i ? { ...x, username: e.target.value } : x)))}
                />
                <Input
                  type="password"
                  placeholder={u.existing ? t`unchanged` : t`password`}
                  value={u.password}
                  onChange={(e) => setUsers(users.map((x, j) => (j === i ? { ...x, password: e.target.value } : x)))}
                  autoComplete="new-password"
                />
                <Button type="button" variant="ghost" size="icon" onClick={() => setUsers(users.filter((_, j) => j !== i))}>
                  <X />
                </Button>
              </div>
            ))}
            <Button type="button" variant="outline" size="sm" onClick={() => setUsers([...users, { username: '', password: '', existing: false }])}>
              <Plus /> <Trans>Add user</Trans>
            </Button>
          </div>
          <div className="space-y-2">
            <Label htmlFor="acl-ips">
              <Trans>Allowed IPs</Trans>
            </Label>
            <textarea
              id="acl-ips"
              className="min-h-20 w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-sm outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
              value={ips}
              onChange={(e) => setIps(e.target.value)}
              placeholder={'203.0.113.7\n192.168.1.0/24'}
            />
            <p className="text-xs text-muted-foreground">
              <Trans>One per line. Empty = any IP.</Trans>
            </p>
          </div>
          {error && (
            <Alert variant="destructive">
              <TriangleAlert />
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={busy}>
              {busy && <Loader2 className="animate-spin" />}
              <Trans>Save</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
