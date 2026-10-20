import { useCallback, useEffect, useState } from 'react';
import { Check, ChevronDown, Copy, ExternalLink, Loader2, Plus, Trash2, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';

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
      toast.success(`${c.username} desconectado de Dockfly`);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  if (!apps) return <p className="py-20 text-center text-sm text-muted-foreground">Cargando…</p>;

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
  if (items.length === 0) return null;
  return (
    <ul className="divide-y rounded-lg border">
      {items.map((c) => (
        <li key={c.id} className="flex items-center gap-3 px-4 py-3">
          <ProviderIcon provider={c.provider} className="size-5" />
          <div className="min-w-0 flex-1">
            <div className="font-medium">{c.username}</div>
            <div className="truncate text-xs text-muted-foreground">
              {c.account_type === 'Organization' ? 'Organización' : 'Cuenta'} · {new URL(c.base_url).host} · conectada{' '}
              {timeAgo(c.created_at)}
            </div>
          </div>
          {c.settings_url && c.auth_type === 'github_app' && (
            <Button variant="ghost" size="sm" asChild>
              <a href={c.settings_url} target="_blank" rel="noreferrer">
                Elegir repositorios <ExternalLink />
              </a>
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon-sm"
            className="text-destructive hover:text-destructive"
            onClick={() => onRemove(c)}
            aria-label={`Desconectar ${c.username}`}
          >
            <Trash2 />
          </Button>
        </li>
      ))}
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
      toast.success(`App ${app.name} quitada de Dockfly`, {
        description: 'Para borrarla del todo, elimínala también en GitHub.',
        action: { label: 'Abrir GitHub', onClick: () => window.open(app.html_url, '_blank') },
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
          Autoriza Dockfly en tu cuenta u organización y elige a qué repositorios puede acceder (solo lectura).
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {apps.length === 0 ? (
          <div className="space-y-3">
            <Button onClick={connect}>
              <ProviderIcon provider="github" /> Conectar con GitHub
            </Button>
            <p className="text-xs text-muted-foreground">
              GitHub te pedirá crear la aplicación «Dockfly» (ya configurada: solo confirmas) y después elegir la cuenta u
              organización y los repositorios.
            </p>
            {!hasPublicURL && (
              <Alert>
                <TriangleAlert />
                <AlertDescription>
                  Aún no hay URL pública: la app se creará sin webhook y los sitios solo se desplegarán con «Desplegar
                  ahora». Puedes configurarla después y Dockfly activará el webhook.
                </AlertDescription>
              </Alert>
            )}
            <button
              type="button"
              onClick={() => setOptions((o) => !o)}
              className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
            >
              <ChevronDown className={cn('size-3.5 transition-transform', options && 'rotate-180')} />
              Crear la app en una organización o en GitHub Enterprise
            </button>
            {options && (
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label htmlFor="gh-org">Organización (opcional)</Label>
                  <Input id="gh-org" value={org} onChange={(e) => setOrg(e.target.value)} placeholder="mi-empresa" />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="gh-enterprise">GitHub Enterprise (opcional)</Label>
                  <Input
                    id="gh-enterprise"
                    value={enterprise}
                    onChange={(e) => setEnterprise(e.target.value)}
                    placeholder="https://github.miempresa.com"
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
                  <Plus /> Añadir cuenta u organización
                </Button>
                <Button variant="ghost" size="sm" asChild>
                  <a href={app.html_url} target="_blank" rel="noreferrer">
                    App {app.name} <ExternalLink />
                  </a>
                </Button>
                {!app.webhook_active && (
                  <span className="text-xs text-amber-600">Sin webhook: configura la URL pública para desplegar en cada push.</span>
                )}
                <Button variant="ghost" size="sm" className="ml-auto text-muted-foreground" onClick={() => removeApp(app)}>
                  Quitar la app
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
        <CardDescription>gitlab.com o tu propia instancia de GitLab.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {apps.length === 0 && !setup && (
          <div className="space-y-2">
            <Button onClick={() => setSetup(true)}>
              <ProviderIcon provider="gitlab" /> Configurar GitLab
            </Button>
            <p className="text-xs text-muted-foreground">
              GitLab no permite crear la aplicación automáticamente: hay que registrarla una vez (un minuto). Después,
              conectar una cuenta es solo pulsar «Autorizar».
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
        {apps.map((app) => (
          <div key={app.id} className="space-y-3">
            <ConnectionList items={connections.filter((c) => c.gitlab_app_id === app.id)} onRemove={onRemove} />
            <div className="flex flex-wrap items-center gap-2">
              <Button size="sm" onClick={() => authorize(app)}>
                <ProviderIcon provider="gitlab" /> Conectar con {new URL(app.web_url).host}
              </Button>
              <Button variant="ghost" size="sm" className="ml-auto text-muted-foreground" onClick={() => removeApp(app)}>
                Quitar la aplicación
              </Button>
            </div>
          </div>
        ))}
        {apps.length > 0 && !setup && (
          <button
            type="button"
            onClick={() => setSetup(true)}
            className="text-xs text-muted-foreground underline-offset-2 hover:underline"
          >
            Añadir otra instancia de GitLab
          </button>
        )}
      </CardContent>
    </Card>
  );
}

function GitLabSetup({ onCancel, onSaved }: { onCancel: () => void; onSaved: () => void }) {
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
      toast.success('GitLab configurado: ya puedes conectar tu cuenta');
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
        <Label htmlFor="gl-url">Instancia de GitLab</Label>
        <Input id="gl-url" value={webURL} onChange={(e) => setWebURL(e.target.value)} placeholder="https://gitlab.com" />
      </div>
      <ol className="list-decimal space-y-2 pl-5 text-sm">
        <li>
          Abre{' '}
          <a
            href={`${base}/-/user_settings/applications`}
            target="_blank"
            rel="noreferrer"
            className="font-medium text-primary hover:underline"
          >
            Aplicaciones en GitLab <ExternalLink className="inline size-3" />
          </a>{' '}
          y pulsa «Add new application».
        </li>
        <li>
          Nombre <strong>Dockfly</strong>, marca <strong>Confidential</strong> y el scope <strong>api</strong>. Como
          Redirect URI pega:
          <CopyField value={redirectURI} />
        </li>
        <li>Guarda y copia aquí el Application ID y el Secret:</li>
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
          Cancelar
        </Button>
        <Button type="submit" disabled={busy}>
          {busy && <Loader2 className="animate-spin" />}
          Guardar
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
        {copied ? <Check /> : <Copy />} {copied ? 'Copiado' : 'Copiar'}
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
          <CardTitle className="text-base">Avanzado: tokens de acceso personal</CardTitle>
        </button>
        {open && (
          <CardDescription>
            Para casos en los que no se puede usar la autorización (p. ej. sin navegador). El token se guarda cifrado.
          </CardDescription>
        )}
        {open && (
          <CardAction className="flex gap-2">
            <Button variant="outline" size="sm" onClick={() => setAdding('github')}>
              <ProviderIcon provider="github" /> Token de GitHub
            </Button>
            <Button variant="outline" size="sm" onClick={() => setAdding('gitlab')}>
              <ProviderIcon provider="gitlab" /> Token de GitLab
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
  const [value, setValue] = useState(initial);
  const [saved, setSaved] = useState(initial);
  const [busy, setBusy] = useState(false);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const res = await api.putSettings({ public_url: value });
      setValue(res.public_url);
      setSaved(res.public_url);
      toast.success('URL pública guardada');
      for (const w of res.warnings ?? []) toast.warning('Webhook de GitHub', { description: w, duration: 15000 });
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
        <CardTitle className="text-lg">URL pública de Dockfly</CardTitle>
        <CardDescription>
          GitHub y GitLab avisan de cada push a esta dirección. Publica el panel con un proxy host (por ejemplo,
          <code className="mx-1 font-mono">dockfly.midominio.com</code> → <code className="font-mono">127.0.0.1:81</code>{' '}
          con SSL) y escríbela aquí. Sin ella, los sitios solo se despliegan con el botón «Desplegar ahora».
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={save} className="flex gap-2">
          <Input
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder="https://dockfly.midominio.com"
            className="max-w-md"
          />
          <Button type="submit" disabled={busy || value === saved}>
            {busy && <Loader2 className="animate-spin" />}
            Guardar
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
        Crea un <strong>fine-grained token</strong> con acceso a los repositorios que quieras desplegar y estos
        permisos: <strong>Contents</strong> (lectura), <strong>Metadata</strong> (lectura) y <strong>Webhooks</strong>{' '}
        (lectura y escritura).
      </>
    ),
  },
  gitlab: {
    url: 'https://gitlab.com/-/user_settings/personal_access_tokens',
    text: (
      <>
        Crea un token de acceso personal con el scope <strong>api</strong> (necesario para registrar el webhook de push).
      </>
    ),
  },
};

function ConnectionDialog({ provider, onClose, onSaved }: { provider: GitProvider; onClose: () => void; onSaved: () => void }) {
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
      toast.success(`${name} vinculado como ${conn.username}`);
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
              <ProviderIcon provider={provider} className="size-5" /> Vincular {name}
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
              Crear el token en {name} <ExternalLink className="size-3.5" />
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
              Se guarda cifrado y nunca llega a los contenedores de build.
            </p>
          </div>

          {selfHosted ? (
            <div className="space-y-2">
              <Label htmlFor="base-url">{provider === 'github' ? 'URL de GitHub Enterprise' : 'URL de tu GitLab'}</Label>
              <Input
                id="base-url"
                value={baseURL}
                onChange={(e) => setBaseURL(e.target.value)}
                placeholder={provider === 'github' ? 'https://github.miempresa.com' : 'https://gitlab.miempresa.com'}
                required
              />
            </div>
          ) : (
            <button
              type="button"
              className="w-fit text-xs text-muted-foreground underline-offset-2 hover:underline"
              onClick={() => setSelfHosted(true)}
            >
              {provider === 'github' ? '¿Usas GitHub Enterprise?' : '¿Usas tu propio GitLab?'}
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
              Cancelar
            </Button>
            <Button type="submit" disabled={busy}>
              {busy ? <Loader2 className="animate-spin" /> : <Plus />}
              Vincular
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
