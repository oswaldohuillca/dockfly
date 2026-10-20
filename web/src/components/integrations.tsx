import { useCallback, useEffect, useState } from 'react';
import { ExternalLink, Loader2, Plus, Trash2, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';

import { ProviderIcon, providerName } from '@/components/brand-icons';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api, errorMessage, type GitConnection, type GitProvider } from '@/lib/api';
import { timeAgo } from '@/lib/format';

export function Integrations() {
  const [connections, setConnections] = useState<GitConnection[]>([]);
  const [adding, setAdding] = useState<GitProvider | null>(null);

  const load = useCallback(async () => {
    try {
      setConnections(await api.listConnections());
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
      toast.success(`Cuenta ${c.username} desvinculada`);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  return (
    <div className="space-y-4">
      <PublicURLCard />

      <Card>
        <CardHeader>
          <CardTitle className="text-lg">Cuentas de Git</CardTitle>
          <CardDescription>Vincula GitHub o GitLab para construir y publicar tus sitios en cada push.</CardDescription>
          <CardAction className="flex gap-2">
            <Button variant="outline" onClick={() => setAdding('github')}>
              <ProviderIcon provider="github" /> GitHub
            </Button>
            <Button variant="outline" onClick={() => setAdding('gitlab')}>
              <ProviderIcon provider="gitlab" /> GitLab
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent>
          {connections.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">Todavía no hay cuentas vinculadas.</p>
          ) : (
            <ul className="divide-y rounded-lg border">
              {connections.map((c) => (
                <li key={c.id} className="flex items-center gap-3 px-4 py-3">
                  <ProviderIcon provider={c.provider} className="size-5" />
                  <div className="min-w-0 flex-1">
                    <div className="font-medium">{c.username}</div>
                    <div className="truncate text-xs text-muted-foreground">
                      {new URL(c.base_url).host} · vinculada {timeAgo(c.created_at)}
                    </div>
                  </div>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="text-destructive hover:text-destructive"
                    onClick={() => remove(c)}
                    aria-label={`Desvincular ${c.username}`}
                  >
                    <Trash2 />
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      {adding && (
        <ConnectionDialog
          provider={adding}
          onClose={() => setAdding(null)}
          onSaved={() => {
            setAdding(null);
            load();
          }}
        />
      )}
    </div>
  );
}

function PublicURLCard() {
  const [value, setValue] = useState('');
  const [saved, setSaved] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.getSettings().then((s) => {
      setValue(s.public_url);
      setSaved(s.public_url);
    });
  }, []);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const res = await api.putSettings({ public_url: value });
      setValue(res.public_url);
      setSaved(res.public_url);
      toast.success('URL pública guardada');
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
