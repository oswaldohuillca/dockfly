import { useCallback, useEffect, useState } from 'react';
import { Check, ChevronDown, Copy, ExternalLink, Loader2, Plus, Trash2, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';
import { Trans, useLingui } from '@lingui/react/macro';

import { ProviderIcon, providerName } from '@/components/brand-icons';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api, errorMessage, type GitConnection, type GitHubApp, type GitLabApp, type GitProvider } from '@/lib/api';
import { goTo, openPopup, postTo, waitForPopup } from '@/lib/oauth-popup';
import { cn } from '@/lib/utils';
import { timeAgo } from '@/lib/format';

export function Integrations() {
  const { t } = useLingui();
  const [connections, setConnections] = useState<GitConnection[]>([]);
  const [apps, setApps] = useState<{ github: GitHubApp[]; gitlab: GitLabApp[] } | null>(null);
  const [publicURL, setPublicURL] = useState('');

  const load = useCallback(async () => {
    try {
      const [conns, a, settings] = await Promise.all([api.listConnections(), api.listGitApps(), api.getSettings()]);
      setConnections(conns);
      setApps(a);
      setPublicURL(settings.public_url);
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  async function remove(c: GitConnection) {
    try {
      await api.deleteConnection(c.id);
      const username = c.username;
      toast.success(t`${username} disconnected from Dockfly`);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  if (!apps) return <p className="py-20 text-center text-sm text-muted-foreground">
        <Trans>Loading…</Trans>
      </p>;

  return (
    <div className="space-y-4">
      <PublicURLCard value={publicURL} onSaved={load} />
      <GitHubCard apps={apps.github} connections={connections} hasPublicURL={!!publicURL} onChange={load} onRemove={remove} />
      <GitLabCard apps={apps.gitlab} connections={connections} onChange={load} onRemove={remove} />
      <TokenCard connections={connections.filter((c) => c.auth_type === 'token')} onChange={load} onRemove={remove} />
    </div>
  );
}

/** Lista de cuentas conectadas. */
function ConnectionList({ items, onRemove }: { items: GitConnection[]; onRemove: (c: GitConnection) => void }) {
  const { t } = useLingui();
  if (items.length === 0) return null;
  return (
    <ul className="divide-y rounded-lg border">
      {items.map((c) => {
        const username = c.username;
        const connectedAgo = timeAgo(c.created_at);
        return (
          <li key={c.id} className="flex items-center gap-3 px-4 py-3">
            <ProviderIcon provider={c.provider} className="size-5" />
            <div className="min-w-0 flex-1">
              <div className="font-medium">{c.username}</div>
              <div className="truncate text-xs text-muted-foreground">
                {c.account_type === 'Organization' ? t`Organization` : t`Account`} · {new URL(c.base_url).host} ·{' '}
                <Trans>connected {connectedAgo}</Trans>
              </div>
            </div>
            {c.settings_url && c.auth_type === 'github_app' && (
              <Button variant="ghost" size="sm" asChild>
                <a href={c.settings_url} target="_blank" rel="noreferrer">
                  <Trans>Choose repositories</Trans> <ExternalLink />
                </a>
              </Button>
            )}
            <Button
              variant="ghost"
              size="icon-sm"
              className="text-destructive hover:text-destructive"
              onClick={() => onRemove(c)}
              aria-label={t`Disconnect ${username}`}
            >
              <Trash2 />
            </Button>
          </li>
        );
      })}
    </ul>
  );
}

/** Ejecuta un flujo en la ventana emergente y avisa del resultado. */
async function runPopup(popup: Window | null, onChange: () => void) {
  const res = await waitForPopup(popup);
  if (res.ok) toast.success(res.message);
  else if (res.message) toast.error(res.message);
  onChange();
}

interface GitHubCardProps {
  apps: GitHubApp[];
  connections: GitConnection[];
  hasPublicURL: boolean;
  onChange: () => void;
  onRemove: (c: GitConnection) => void;
}

function GitHubCard({ apps, connections, hasPublicURL, onChange, onRemove }: GitHubCardProps) {
  const { t } = useLingui();
  const [options, setOptions] = useState(false);
  const [org, setOrg] = useState('');
  const [enterprise, setEnterprise] = useState('');

  async function connect() {
    const popup = openPopup(); // antes de cualquier await, o el navegador la bloquea
    try {
      const res = await api.startGitHubManifest({ panel_url: location.origin, organization: org, enterprise_url: enterprise });
      postTo(popup, res.action, { manifest: res.manifest });
      runPopup(popup, onChange);
    } catch (e) {
      popup?.close();
      toast.error(errorMessage(e));
    }
  }

  async function addAccount(app: GitHubApp) {
    const popup = openPopup();
    try {
      goTo(popup, (await api.githubInstallURL(app.id, location.origin)).url);
      runPopup(popup, onChange);
    } catch (e) {
      popup?.close();
      toast.error(errorMessage(e));
    }
  }

  async function removeApp(app: GitHubApp) {
    try {
      await api.deleteGitHubApp(app.id);
      const name = app.name;
      toast.success(t`App ${name} removed from Dockfly`, {
        description: t`To delete it completely, remove it on GitHub too.`,
        action: { label: t`Open GitHub`, onClick: () => window.open(app.html_url, '_blank') },
      });
      onChange();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-lg">
          <ProviderIcon provider="github" className="size-5" /> GitHub
        </CardTitle>
        <CardDescription>
          <Trans>
            Authorize Dockfly on your account or organization and choose which repositories it can access (read-only).
          </Trans>
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {apps.length === 0 ? (
          <div className="space-y-3">
            <Button onClick={connect}>
              <ProviderIcon provider="github" /> <Trans>Connect with GitHub</Trans>
            </Button>
            <p className="text-xs text-muted-foreground">
              <Trans>
                GitHub will ask you to create the “Dockfly” app (already configured: just confirm) and then choose the
                account or organization and the repositories.
              </Trans>
            </p>
            {!hasPublicURL && (
              <Alert>
                <TriangleAlert />
                <AlertDescription>
                  <Trans>
                    No public URL yet: the app will be created without a webhook and sites will only deploy with Deploy
                    now. You can set it later and Dockfly will enable the webhook.
                  </Trans>
                </AlertDescription>
              </Alert>
            )}
            <button
              type="button"
              onClick={() => setOptions((o) => !o)}
              className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
            >
              <ChevronDown className={cn('size-3.5 transition-transform', options && 'rotate-180')} />
              <Trans>Create the app in an organization or on GitHub Enterprise</Trans>
            </button>
            {options && (
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label htmlFor="gh-org">
                    <Trans>Organization (optional)</Trans>
                  </Label>
                  <Input id="gh-org" value={org} onChange={(e) => setOrg(e.target.value)} placeholder={t`my-company`} />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="gh-enterprise">
                    <Trans>GitHub Enterprise (optional)</Trans>
                  </Label>
                  <Input
                    id="gh-enterprise"
                    value={enterprise}
                    onChange={(e) => setEnterprise(e.target.value)}
                    placeholder="https://github.mycompany.com"
                  />
                </div>
              </div>
            )}
          </div>
        ) : (
          apps.map((app) => (
            <div key={app.id} className="space-y-3">
              <ConnectionList items={connections.filter((c) => c.github_app_id === app.id)} onRemove={onRemove} />
              <div className="flex flex-wrap items-center gap-2">
                <Button variant="outline" size="sm" onClick={() => addAccount(app)}>
                  <Plus /> <Trans>Add account or organization</Trans>
                </Button>
                <Button variant="ghost" size="sm" asChild>
                  <a href={app.html_url} target="_blank" rel="noreferrer">
                    <Trans>App {app.name}</Trans> <ExternalLink />
                  </a>
                </Button>
                {!app.webhook_active && (
                  <span className="text-xs text-amber-600">
                    <Trans>No webhook: set the public URL to deploy on every push.</Trans>
                  </span>
                )}
                <Button variant="ghost" size="sm" className="ml-auto text-muted-foreground" onClick={() => removeApp(app)}>
                  <Trans>Remove app</Trans>
                </Button>
              </div>
            </div>
          ))
        )}
      </CardContent>
    </Card>
  );
}

interface GitLabCardProps {
  apps: GitLabApp[];
  connections: GitConnection[];
  onChange: () => void;
  onRemove: (c: GitConnection) => void;
}

function GitLabCard({ apps, connections, onChange, onRemove }: GitLabCardProps) {
  const [setup, setSetup] = useState(false);

  async function authorize(app: GitLabApp) {
    const popup = openPopup();
    try {
      goTo(popup, (await api.gitlabAuthorize(app.id, location.origin)).url);
      runPopup(popup, onChange);
    } catch (e) {
      popup?.close();
      toast.error(errorMessage(e));
    }
  }

  async function removeApp(app: GitLabApp) {
    try {
      await api.deleteGitLabApp(app.id);
      onChange();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-lg">
          <ProviderIcon provider="gitlab" className="size-5" /> GitLab
        </CardTitle>
        <CardDescription>
          <Trans>gitlab.com or your own GitLab instance.</Trans>
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {apps.length === 0 && !setup && (
          <div className="space-y-2">
            <Button onClick={() => setSetup(true)}>
              <ProviderIcon provider="gitlab" /> <Trans>Set up GitLab</Trans>
            </Button>
            <p className="text-xs text-muted-foreground">
              <Trans>
                GitLab doesn't allow creating the app automatically: you need to register it once (takes a minute).
                After that, connecting an account is just a click on Connect.
              </Trans>
            </p>
          </div>
        )}
        {setup && (
          <GitLabSetup
            onCancel={() => setSetup(false)}
            onSaved={() => {
              setSetup(false);
              onChange();
            }}
          />
        )}
        {apps.map((app) => {
          const host = new URL(app.web_url).host;
          return (
            <div key={app.id} className="space-y-3">
              <ConnectionList items={connections.filter((c) => c.gitlab_app_id === app.id)} onRemove={onRemove} />
              <div className="flex flex-wrap items-center gap-2">
                <Button size="sm" onClick={() => authorize(app)}>
                  <ProviderIcon provider="gitlab" /> <Trans>Connect with {host}</Trans>
                </Button>
                <Button variant="ghost" size="sm" className="ml-auto text-muted-foreground" onClick={() => removeApp(app)}>
                  <Trans>Remove app</Trans>
                </Button>
              </div>
            </div>
          );
        })}
        {apps.length > 0 && !setup && (
          <button
            type="button"
            onClick={() => setSetup(true)}
            className="text-xs text-muted-foreground underline-offset-2 hover:underline"
          >
            <Trans>Add another GitLab instance</Trans>
          </button>
        )}
      </CardContent>
    </Card>
  );
}

function GitLabSetup({ onCancel, onSaved }: { onCancel: () => void; onSaved: () => void }) {
  const { t } = useLingui();
  const [webURL, setWebURL] = useState('https://gitlab.com');
  const [clientID, setClientID] = useState('');
  const [secret, setSecret] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const redirectURI = `${location.origin}/api/git/gitlab/callback`;
  const base = webURL.replace(/\/+$/, '') || 'https://gitlab.com';

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      await api.putGitLabApp({ web_url: webURL, client_id: clientID, client_secret: secret });
      toast.success(t`GitLab is set up: you can now connect your account`);
      onSaved();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={save} className="space-y-4 rounded-lg border p-4">
      <div className="space-y-2">
        <Label htmlFor="gl-url">
          <Trans>GitLab instance</Trans>
        </Label>
        <Input id="gl-url" value={webURL} onChange={(e) => setWebURL(e.target.value)} placeholder="https://gitlab.com" />
      </div>
      <ol className="list-decimal space-y-2 pl-5 text-sm">
        <li>
          <Trans>
            Open{' '}
            <a
              href={`${base}/-/user_settings/applications`}
              target="_blank"
              rel="noreferrer"
              className="font-medium text-primary hover:underline"
            >
              Applications in GitLab <ExternalLink className="inline size-3" />
            </a>{' '}
            and click “Add new application”.
          </Trans>
        </li>
        <li>
          <Trans>
            Name it <strong>Dockfly</strong>, check <strong>Confidential</strong> and the <strong>api</strong> scope.
            Paste this as the Redirect URI:
          </Trans>
          <CopyField value={redirectURI} />
        </li>
        <li>
          <Trans>Save, then copy the Application ID and Secret here:</Trans>
        </li>
      </ol>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-2">
          <Label htmlFor="gl-id">Application ID</Label>
          <Input id="gl-id" value={clientID} onChange={(e) => setClientID(e.target.value)} required className="font-mono" />
        </div>
        <div className="space-y-2">
          <Label htmlFor="gl-secret">Secret</Label>
          <Input
            id="gl-secret"
            type="password"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            required
            autoComplete="off"
            className="font-mono"
          />
        </div>
      </div>
      {error && (
        <Alert variant="destructive">
          <TriangleAlert />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onCancel}>
          <Trans>Cancel</Trans>
        </Button>
        <Button type="submit" disabled={busy}>
          {busy && <Loader2 className="animate-spin" />}
          <Trans>Save</Trans>
        </Button>
      </div>
    </form>
  );
}

function CopyField({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="mt-1.5 flex items-center gap-2">
      <code className="min-w-0 flex-1 truncate rounded bg-muted px-2 py-1.5 font-mono text-xs">{value}</code>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={async () => {
          await navigator.clipboard.writeText(value);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        }}
      >
        {copied ? <Check /> : <Copy />} {copied ? <Trans>Copied</Trans> : <Trans>Copy</Trans>}
      </Button>
    </div>
  );
}

/** Tokens de acceso personal: opción avanzada. */
function TokenCard({ connections, onChange, onRemove }: { connections: GitConnection[]; onChange: () => void; onRemove: (c: GitConnection) => void }) {
  const [open, setOpen] = useState(connections.length > 0);
  const [adding, setAdding] = useState<GitProvider | null>(null);

  return (
    <Card>
      <CardHeader>
        <button type="button" onClick={() => setOpen((o) => !o)} className="flex items-center gap-2 text-left">
          <ChevronDown className={cn('size-4 text-muted-foreground transition-transform', open && 'rotate-180')} />
          <CardTitle className="text-base">
            <Trans>Advanced: personal access tokens</Trans>
          </CardTitle>
        </button>
        {open && (
          <CardDescription>
            <Trans>
              For cases where authorization can't be used (e.g. without a browser). The token is stored encrypted.
            </Trans>
          </CardDescription>
        )}
        {open && (
          <CardAction className="flex gap-2">
            <Button variant="outline" size="sm" onClick={() => setAdding('github')}>
              <ProviderIcon provider="github" /> <Trans>GitHub token</Trans>
            </Button>
            <Button variant="outline" size="sm" onClick={() => setAdding('gitlab')}>
              <ProviderIcon provider="gitlab" /> <Trans>GitLab token</Trans>
            </Button>
          </CardAction>
        )}
      </CardHeader>
      {open && connections.length > 0 && (
        <CardContent>
          <ConnectionList items={connections} onRemove={onRemove} />
        </CardContent>
      )}
      {adding && (
        <ConnectionDialog
          provider={adding}
          onClose={() => setAdding(null)}
          onSaved={() => {
            setAdding(null);
            onChange();
          }}
        />
      )}
    </Card>
  );
}

function PublicURLCard({ value: initial, onSaved }: { value: string; onSaved: () => void }) {
  const { t } = useLingui();
  const [value, setValue] = useState(initial);
  const [saved, setSaved] = useState(initial);
  const [busy, setBusy] = useState(false);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const res = await api.putSettings({ public_url: value });
      setValue(res.settings.public_url);
      setSaved(res.settings.public_url);
      toast.success(t`Public URL saved`);
      for (const w of res.warnings ?? []) toast.warning(t`GitHub webhook`, { description: w, duration: 15000 });
      onSaved();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">
          <Trans>Dockfly public URL</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>
            GitHub and GitLab notify this address on every push. Expose the panel with a proxy host (for example,
            <code className="mx-1 font-mono">dockfly.mydomain.com</code> → <code className="font-mono">127.0.0.1:81</code>{' '}
            with SSL) and enter it here. Without it, sites only deploy with the Deploy now button.
          </Trans>
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={save} className="flex gap-2">
          <Input
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder="https://dockfly.mydomain.com"
            className="max-w-md"
          />
          <Button type="submit" disabled={busy || value === saved}>
            {busy && <Loader2 className="animate-spin" />}
            <Trans>Save</Trans>
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}

const tokenHelp: Record<GitProvider, { url: string; text: React.ReactNode }> = {
  github: {
    url: 'https://github.com/settings/personal-access-tokens/new',
    text: (
      <>
        <Trans>
          Create a <strong>fine-grained token</strong> with access to the repositories you want to deploy and these
          permissions: <strong>Contents</strong> (read), <strong>Metadata</strong> (read) and <strong>Webhooks</strong>{' '}
          (read and write).
        </Trans>
      </>
    ),
  },
  gitlab: {
    url: 'https://gitlab.com/-/user_settings/personal_access_tokens',
    text: (
      <>
        <Trans>
          Create a personal access token with the <strong>api</strong> scope (required to register the push webhook).
        </Trans>
      </>
    ),
  },
};

function ConnectionDialog({ provider, onClose, onSaved }: { provider: GitProvider; onClose: () => void; onSaved: () => void }) {
  const { t } = useLingui();
  const [token, setToken] = useState('');
  const [baseURL, setBaseURL] = useState('');
  const [selfHosted, setSelfHosted] = useState(false);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const help = tokenHelp[provider];
  const name = providerName[provider];

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      const conn = await api.createConnection({ provider, base_url: selfHosted ? baseURL : '', token });
      const username = conn.username;
      toast.success(t`${name} connected as ${username}`);
      onSaved();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const tokenPage = selfHosted && baseURL ? null : help.url;

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={save} className="contents">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <ProviderIcon provider={provider} className="size-5" /> <Trans>Connect {name}</Trans>
            </DialogTitle>
            <DialogDescription>{help.text}</DialogDescription>
          </DialogHeader>

          {tokenPage && (
            <a
              href={tokenPage}
              target="_blank"
              rel="noreferrer"
              className="flex items-center gap-1 text-sm font-medium text-primary hover:underline"
            >
              <Trans>Create the token on {name}</Trans> <ExternalLink className="size-3.5" />
            </a>
          )}

          <div className="space-y-2">
            <Label htmlFor="token">Token</Label>
            <Input
              id="token"
              type="password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder={provider === 'github' ? 'github_pat_…' : 'glpat-…'}
              autoComplete="off"
              required
              autoFocus
            />
            <p className="text-xs text-muted-foreground">
              <Trans>Stored encrypted and never exposed to build containers.</Trans>
            </p>
          </div>

          {selfHosted ? (
            <div className="space-y-2">
              <Label htmlFor="base-url">{provider === 'github' ? <Trans>GitHub Enterprise URL</Trans> : <Trans>Your GitLab URL</Trans>}</Label>
              <Input
                id="base-url"
                value={baseURL}
                onChange={(e) => setBaseURL(e.target.value)}
                placeholder={provider === 'github' ? 'https://github.mycompany.com' : 'https://gitlab.mycompany.com'}
                required
              />
            </div>
          ) : (
            <button
              type="button"
              className="w-fit text-xs text-muted-foreground underline-offset-2 hover:underline"
              onClick={() => setSelfHosted(true)}
            >
              {provider === 'github' ? <Trans>Using GitHub Enterprise?</Trans> : <Trans>Using your own GitLab?</Trans>}
            </button>
          )}

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
              {busy ? <Loader2 className="animate-spin" /> : <Plus />}
              <Trans>Connect</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
