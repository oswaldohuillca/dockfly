import { useCallback, useContext, useEffect, useState } from 'react';
import { Copy, KeyRound, Loader2, Plus, Trash2 } from 'lucide-react';
import { Trans, useLingui } from '@lingui/react/macro';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { api, errorMessage, type APIToken, type Role } from '@/lib/api';
import { formatDate, timeAgo } from '@/lib/format';
import { can, roleHint, roleLabel, UserContext } from '@/lib/roles';

/** Tokens de API del usuario, para el CLI y la integración continua. */
export function APITokens() {
  const { t } = useLingui();
  const [tokens, setTokens] = useState<APIToken[]>([]);
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<string | null>(null);

  const load = useCallback(() => {
    api
      .listTokens()
      .then(setTokens)
      .catch((e) => toast.error(errorMessage(e)));
  }, []);
  useEffect(load, [load]);

  async function revoke(token: APIToken) {
    try {
      await api.deleteToken(token.id);
      const name = token.name;
      toast.success(t`Token "${name}" revoked`);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">
          <Trans>API tokens</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>
            For deploying from the terminal or CI with <code className="font-mono">dockfly deploy</code>. They act on
            your behalf, with the role you choose (never more than your own).
          </Trans>
        </CardDescription>
        <CardAction>
          <Button variant="outline" onClick={() => setCreating(true)}>
            <Plus /> <Trans>Create token</Trans>
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="px-0">
        {tokens.length === 0 ? (
          <p className="px-6 py-2 text-sm text-muted-foreground">
            <Trans>You don't have any tokens yet.</Trans>
          </p>
        ) : (
          <ul className="divide-y">
            {tokens.map((token) => {
              const expired = token.expires_at !== null && new Date(token.expires_at) < new Date();
              return (
                <li key={token.id} className="flex flex-wrap items-center gap-3 px-6 py-3">
                  <KeyRound className="size-4 text-muted-foreground" />
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2 font-medium">
                      {token.name}
                      <code className="font-mono text-xs font-normal text-muted-foreground">{token.prefix}…</code>
                      {expired && (
                        <Badge variant="destructive">
                          <Trans>Expired</Trans>
                        </Badge>
                      )}
                    </div>
                    <div className="text-xs text-muted-foreground">
                      {token.last_used_at ? (
                        <Trans>Used {timeAgo(token.last_used_at)}</Trans>
                      ) : (
                        <Trans>Never used</Trans>
                      )}{' '}
                      ·{' '}
                      {token.expires_at ? (
                        <Trans>expires on {formatDate(token.expires_at)}</Trans>
                      ) : (
                        <Trans>never expires</Trans>
                      )}
                    </div>
                  </div>
                  <Badge variant="secondary">{roleLabel(token.role)}</Badge>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="text-destructive hover:text-destructive"
                    onClick={() => revoke(token)}
                    aria-label={t`Revoke token ${token.name}`}
                  >
                    <Trash2 />
                  </Button>
                </li>
              );
            })}
          </ul>
        )}
      </CardContent>
      {creating && (
        <CreateTokenDialog
          onClose={() => setCreating(false)}
          onCreated={(token) => {
            setCreating(false);
            setCreated(token);
            load();
          }}
        />
      )}
      {created && <TokenCreatedDialog token={created} onClose={() => setCreated(null)} />}
    </Card>
  );
}

function CreateTokenDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (token: string) => void }) {
  const user = useContext(UserContext);
  const roles = (['deployer', 'viewer', 'admin'] as const).filter((r) => can(user, r));
  const [name, setName] = useState('');
  const [role, setRole] = useState<Role>(roles[0] ?? 'viewer');
  const [days, setDays] = useState('90');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      onCreated((await api.createToken({ name, role, expires_days: Number(days) })).token);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={save} className="contents">
          <DialogHeader>
            <DialogTitle>
              <Trans>Create API token</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>Give it a name that tells you where it's used, so you can revoke it later.</Trans>
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="token-name">
              <Trans>Name</Trans>
            </Label>
            <Input
              id="token-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="GitHub Actions"
              required
              autoFocus
            />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label>
                <Trans>Permissions</Trans>
              </Label>
              <Select value={role} onValueChange={(v) => setRole(v as Role)}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {roles.map((r) => (
                    <SelectItem key={r} value={r}>
                      {roleLabel(r)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label>
                <Trans>Expires</Trans>
              </Label>
              <Select value={days} onValueChange={setDays}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="30">
                    <Trans>In 30 days</Trans>
                  </SelectItem>
                  <SelectItem value="90">
                    <Trans>In 90 days</Trans>
                  </SelectItem>
                  <SelectItem value="365">
                    <Trans>In a year</Trans>
                  </SelectItem>
                  <SelectItem value="0">
                    <Trans>Never</Trans>
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
          <p className="text-xs text-muted-foreground">{roleHint(role)}</p>
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={busy}>
              {busy && <Loader2 className="animate-spin" />}
              <Trans>Create</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function TokenCreatedDialog({ token, onClose }: { token: string; onClose: () => void }) {
  const { t } = useLingui();
  const example = `DOCKFLY_URL=${window.location.origin} \\
DOCKFLY_TOKEN=${token} \\
dockfly deploy ./dist --site mi-web`;
  const dockerCommand =
    'docker run --rm -v $PWD/dist:/dist -e DOCKFLY_URL -e DOCKFLY_TOKEN ghcr.io/oswaldohuillca/dockfly deploy /dist --site mi-web';
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>
            <Trans>Copy your token</Trans>
          </DialogTitle>
          <DialogDescription>
            <Trans>It won't be shown again. Store it as a secret (e.g. in your GitHub Actions secrets).</Trans>
          </DialogDescription>
        </DialogHeader>
        <div className="flex gap-2">
          <Input readOnly value={token} className="font-mono text-xs" onFocus={(e) => e.target.select()} />
          <Button variant="outline" onClick={() => navigator.clipboard.writeText(token).then(() => toast.success(t`Token copied`))}>
            <Copy /> <Trans>Copy</Trans>
          </Button>
        </div>
        <div className="space-y-1.5">
          <p className="text-sm font-medium">
            <Trans>Deploy a folder</Trans>
          </p>
          <pre className="overflow-x-auto rounded-lg bg-muted p-3 font-mono text-xs">{example}</pre>
          <p className="text-xs text-muted-foreground">
            <Trans>
              The <code className="font-mono">dockfly</code> binary is in the project's releases, or use it from the
              image: <code className="font-mono">{dockerCommand}</code>
            </Trans>
          </p>
        </div>
        <DialogFooter>
          <Button onClick={onClose}>
            <Trans>Done</Trans>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
