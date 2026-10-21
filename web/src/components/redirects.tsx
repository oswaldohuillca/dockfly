import { useCallback, useEffect, useState } from 'react';
import { ArrowRight, Loader2, Pencil, Plus, Trash2, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';
import { Trans, useLingui } from '@lingui/react/macro';

import { CertBadge } from '@/components/cert-badge';
import { CheckField } from '@/components/check-field';
import { DomainsInput } from '@/components/domains-input';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { api, errorMessage, type RedirectHost, type RedirectInput } from '@/lib/api';
import { useCertificates } from '@/lib/use-certificates';
import { cn } from '@/lib/utils';
import { useCan } from '@/lib/roles';

export function Redirects() {
  const { t } = useLingui();
  const canEdit = useCan('admin');
  const [list, setList] = useState<RedirectHost[]>([]);
  const [editing, setEditing] = useState<RedirectHost | 'new' | null>(null);
  const certs = useCertificates();
  const load = useCallback(() => {
    api.listRedirects().then(setList).catch((e) => toast.error(errorMessage(e)));
  }, []);
  useEffect(load, [load]);

  const input = (r: RedirectHost): RedirectInput => ({
    domains: r.domains, target: r.target, permanent: r.permanent, preserve_path: r.preserve_path, ssl: r.ssl, enabled: r.enabled,
  });

  async function toggle(r: RedirectHost) {
    try {
      await api.updateRedirect(r.id, { ...input(r), enabled: !r.enabled });
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  async function remove(r: RedirectHost) {
    try {
      await api.deleteRedirect(r.id);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">
          <Trans>Redirects</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>Send a domain to another URL: www → apex, an old domain to a new one…</Trans>
        </CardDescription>
        <CardAction>
          {canEdit && (
            <Button variant="outline" onClick={() => setEditing('new')}>
              <Plus /> <Trans>Add</Trans>
            </Button>
          )}
        </CardAction>
      </CardHeader>
      {list.length > 0 && (
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="pl-6">
                  <Trans>Domains</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Target</Trans>
                </TableHead>
                <TableHead>SSL</TableHead>
                <TableHead>
                  <Trans context="redirect">Enabled</Trans>
                </TableHead>
                <TableHead className="pr-6" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((r) => (
                <TableRow key={r.id} className={cn(!r.enabled && 'opacity-55')}>
                  <TableCell className="pl-6 font-medium">{r.domains.join(', ')}</TableCell>
                  <TableCell>
                    <div className="flex items-center gap-1.5">
                      <ArrowRight className="size-3.5 text-muted-foreground" />
                      <code className="font-mono text-xs">
                        {r.target}
                        {r.preserve_path && '/…'}
                      </code>
                      <Badge variant="outline" className="font-normal">
                        {r.permanent ? '301' : '302'}
                      </Badge>
                    </div>
                  </TableCell>
                  <TableCell>
                    {r.ssl ? (
                      <CertBadge domains={r.domains} certs={certs} />
                    ) : (
                      <Badge variant="outline" className="text-muted-foreground">
                        HTTP
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell>
                    <Switch checked={r.enabled} onCheckedChange={() => toggle(r)} aria-label={t({ message: 'Enabled', context: 'redirect' })} />
                  </TableCell>
                  <TableCell className="pr-6 text-right">
                    <Button variant="ghost" size="icon-sm" onClick={() => setEditing(r)} aria-label={t`Edit`}>
                      <Pencil />
                    </Button>
                    <Button variant="ghost" size="icon-sm" className="text-destructive hover:text-destructive" onClick={() => remove(r)} aria-label={t`Delete`}>
                      <Trash2 />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      )}
      {editing && (
        <RedirectDialog
          redirect={editing === 'new' ? undefined : editing}
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

function RedirectDialog({ redirect, onClose, onSaved }: { redirect?: RedirectHost; onClose: () => void; onSaved: () => void }) {
  const [form, setForm] = useState<RedirectInput>(() => ({
    domains: redirect?.domains ?? [], target: redirect?.target ?? '', permanent: redirect?.permanent ?? true,
    preserve_path: redirect?.preserve_path ?? true, ssl: redirect?.ssl ?? true, enabled: redirect?.enabled ?? true,
  }));
  const { t } = useLingui();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const set = <K extends keyof RedirectInput>(k: K, v: RedirectInput[K]) => setForm((f) => ({ ...f, [k]: v }));

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      if (redirect) await api.updateRedirect(redirect.id, form);
      else await api.createRedirect(form);
      toast.success(t`Redirect saved`);
      onSaved();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const example = form.domains[0] ?? 'old.com';
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={save} className="contents">
          <DialogHeader>
            <DialogTitle>{redirect ? <Trans>Edit redirect</Trans> : <Trans>New redirect</Trans>}</DialogTitle>
            <DialogDescription>
              <Trans>Traefik responds directly, with no server behind it.</Trans>
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="rd-domains">
              <Trans>Source domains</Trans>
            </Label>
            <DomainsInput id="rd-domains" value={form.domains} onChange={(d) => set('domains', d)} />
          </div>
          <div className="space-y-2">
            <Label htmlFor="rd-target">
              <Trans>Target</Trans>
            </Label>
            <Input id="rd-target" className="font-mono" value={form.target} onChange={(e) => set('target', e.target.value)} placeholder="https://new.com" required />
          </div>
          <CheckField
            id="rd-path"
            checked={form.preserve_path}
            onChange={(v) => set('preserve_path', v)}
            label={t`Preserve path`}
            hint={`${example}/blog/post → ${form.target || 'https://new.com'}/blog/post`}
          />
          <CheckField
            id="rd-perm"
            checked={form.permanent}
            onChange={(v) => set('permanent', v)}
            label={t`Permanent (301)`}
            hint={t`Search engines and browsers remember the change. Turn it off (302) if the redirect is temporary.`}
          />
          <CheckField
            id="rd-ssl"
            checked={form.ssl}
            onChange={(v) => set('ssl', v)}
            label={t`SSL certificate for the source domain`}
            hint={t`Required for https://source-domain to work before redirecting.`}
          />
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
