import { useEffect, useMemo, useState } from 'react';
import { Link } from '@tanstack/react-router';
import { ChevronDown, Loader2, Lock, Plus, Search, TriangleAlert, X } from 'lucide-react';
import { toast } from 'sonner';

import { ProviderIcon } from '@/components/brand-icons';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { api, errorMessage, type GitConnection, type Repo, type Site, type SiteGitInput } from '@/lib/api';
import { DEFAULT_IMAGE, matchPreset, presets } from '@/lib/presets';
import { cn } from '@/lib/utils';

interface Props {
  site: Site;
  onClose: () => void;
  onSaved: (webhookError: string) => void;
}

type EnvRow = { key: string; value: string };

export function GitDialog({ site, onClose, onSaved }: Props) {
  const linked = site.repo !== '';
  const [connections, setConnections] = useState<GitConnection[] | null>(null);
  const [form, setForm] = useState<Omit<SiteGitInput, 'env'>>(() => ({
    connection_id: site.git_connection_id ?? 0,
    repo: site.repo,
    branch: site.branch,
    root_dir: site.root_dir,
    // Un sitio nuevo empieza con el preset de Vite.
    install_command: linked ? site.install_command : presets[0].install,
    build_command: linked ? site.build_command : presets[0].build,
    output_dir: linked ? site.output_dir : presets[0].output,
    build_image: site.build_image || DEFAULT_IMAGE,
  }));
  const [env, setEnv] = useState<EnvRow[]>([]);
  const [envTouched, setEnvTouched] = useState(false);
  const [repos, setRepos] = useState<Repo[] | null>(null);
  const [repoQuery, setRepoQuery] = useState('');
  const [branches, setBranches] = useState<string[]>([]);
  const [advanced, setAdvanced] = useState(linked && (site.root_dir !== '' || site.build_image !== DEFAULT_IMAGE));
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const set = <K extends keyof typeof form>(key: K, value: (typeof form)[K]) => setForm((f) => ({ ...f, [key]: value }));

  useEffect(() => {
    api.listConnections().then((list) => {
      setConnections(list);
      setForm((f) => (f.connection_id === 0 && list.length > 0 ? { ...f, connection_id: list[0].id } : f));
    });
    if (linked) {
      api.getSiteEnv(site.id).then((e) => setEnv(Object.entries(e).map(([key, value]) => ({ key, value }))));
    }
  }, [linked, site.id]);

  useEffect(() => {
    if (!form.connection_id) return;
    setRepos(null);
    api
      .listRepos(form.connection_id)
      .then(setRepos)
      .catch((e) => {
        setRepos([]);
        setError(errorMessage(e));
      });
  }, [form.connection_id]);

  useEffect(() => {
    if (!form.connection_id || !form.repo) return;
    api
      .listBranches(form.connection_id, form.repo)
      .then(setBranches)
      .catch(() => setBranches([]));
  }, [form.connection_id, form.repo]);

  const visibleRepos = useMemo(() => {
    const q = repoQuery.trim().toLowerCase();
    return (repos ?? []).filter((r) => !q || r.full_name.toLowerCase().includes(q));
  }, [repos, repoQuery]);

  const preset = matchPreset(form.install_command, form.build_command, form.output_dir);

  function pickRepo(r: Repo) {
    setForm((f) => ({ ...f, repo: r.full_name, branch: r.default_branch }));
  }

  function applyPreset(id: string) {
    const p = presets.find((p) => p.id === id);
    if (p) setForm((f) => ({ ...f, install_command: p.install, build_command: p.build, output_dir: p.output }));
  }

  async function clearCache() {
    try {
      await api.clearBuildCache(site.id);
      toast.success('Caché vaciada: el próximo build descargará las dependencias de nuevo');
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  function editEnv(rows: EnvRow[]) {
    setEnv(rows);
    setEnvTouched(true);
  }

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      const envMap = Object.fromEntries(env.filter((r) => r.key.trim()).map((r) => [r.key.trim(), r.value]));
      const res = await api.putSiteGit(site.id, { ...form, env: envTouched || !linked ? envMap : null });
      toast.success(linked ? 'Configuración guardada' : `${form.repo} conectado`);
      onSaved(res.webhook_error);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (connections && connections.length === 0) {
    return (
      <Dialog open onOpenChange={(open) => !open && onClose()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Primero vincula una cuenta</DialogTitle>
            <DialogDescription>
              Para construir desde un repositorio necesitas vincular tu cuenta de GitHub o GitLab.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={onClose}>
              Cancelar
            </Button>
            <Button asChild>
              <Link to="/integrations">Ir a Integraciones</Link>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    );
  }

  const conn = connections?.find((c) => c.id === form.connection_id);

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-2xl">
        <form onSubmit={save} className="contents">
          <DialogHeader>
            <DialogTitle>{linked ? 'Configurar el build' : 'Conectar un repositorio'}</DialogTitle>
            <DialogDescription>
              Cada push a la rama elegida construye el sitio en un contenedor aislado y publica el resultado.
            </DialogDescription>
          </DialogHeader>

          <div className="grid gap-4 sm:grid-cols-[1fr_1.4fr]">
            <div className="space-y-2">
              <Label>Cuenta</Label>
              <Select value={String(form.connection_id || '')} onValueChange={(v) => set('connection_id', Number(v))}>
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="Cargando…" />
                </SelectTrigger>
                <SelectContent>
                  {connections?.map((c) => (
                    <SelectItem key={c.id} value={String(c.id)}>
                      <ProviderIcon provider={c.provider} /> {c.username}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label>Rama de producción</Label>
              <Select value={form.branch} onValueChange={(v) => set('branch', v)} disabled={!form.repo}>
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="Elige un repositorio" />
                </SelectTrigger>
                <SelectContent>
                  {(branches.includes(form.branch) || !form.branch ? branches : [form.branch, ...branches]).map((b) => (
                    <SelectItem key={b} value={b}>
                      {b}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="space-y-2">
            <Label>Repositorio</Label>
            <div className="rounded-md border">
              <div className="relative border-b">
                <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
                <input
                  className="h-9 w-full bg-transparent pr-3 pl-8 text-sm outline-none placeholder:text-muted-foreground"
                  placeholder="Buscar…"
                  value={repoQuery}
                  onChange={(e) => setRepoQuery(e.target.value)}
                />
              </div>
              <ul className="max-h-44 overflow-y-auto p-1">
                {repos === null ? (
                  <li className="px-2 py-3 text-center text-sm text-muted-foreground">Cargando repositorios…</li>
                ) : visibleRepos.length === 0 ? (
                  <li className="px-2 py-3 text-center text-sm text-muted-foreground">
                    Ningún repositorio{repoQuery && ` coincide con «${repoQuery}»`}. Revisa a qué repos tiene acceso el
                    token.
                  </li>
                ) : (
                  visibleRepos.map((r) => (
                    <li key={r.full_name}>
                      <button
                        type="button"
                        onClick={() => pickRepo(r)}
                        className={cn(
                          'flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm hover:bg-muted',
                          r.full_name === form.repo && 'bg-primary/10 font-medium',
                        )}
                      >
                        {conn && <ProviderIcon provider={conn.provider} className="size-3.5 opacity-70" />}
                        <span className="truncate">{r.full_name}</span>
                        {r.private && <Lock className="size-3 shrink-0 text-muted-foreground" />}
                      </button>
                    </li>
                  ))
                )}
              </ul>
            </div>
          </div>

          <div className="space-y-2">
            <Label>Framework</Label>
            <Select value={preset?.id ?? 'custom'} onValueChange={applyPreset}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {presets.map((p) => (
                  <SelectItem key={p.id} value={p.id}>
                    {p.label}
                  </SelectItem>
                ))}
                <SelectItem value="custom" disabled>
                  Personalizado
                </SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div className="grid gap-4 sm:grid-cols-3">
            <Field label="Instalar" value={form.install_command} onChange={(v) => set('install_command', v)} placeholder="bun install" mono />
            <Field label="Build" value={form.build_command} onChange={(v) => set('build_command', v)} placeholder="bun run build" mono />
            <Field label="Directorio de salida" value={form.output_dir} onChange={(v) => set('output_dir', v)} placeholder="dist" mono />
          </div>
          {!form.install_command && !form.build_command && (
            <p className="-mt-2 text-xs text-muted-foreground">
              Sin comandos se publican directamente los archivos del repositorio (o de su directorio de salida).
            </p>
          )}

          <button
            type="button"
            onClick={() => setAdvanced((a) => !a)}
            className="flex w-fit items-center gap-1 text-sm font-medium text-muted-foreground hover:text-foreground"
          >
            <ChevronDown className={cn('size-4 transition-transform', advanced && 'rotate-180')} /> Opciones avanzadas
          </button>
          {advanced && linked && (
            <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2 text-sm">
              <span className="text-muted-foreground">
                Las dependencias se guardan en caché entre builds. Vacíala si ves errores extraños al instalar.
              </span>
              <Button type="button" variant="outline" size="sm" onClick={clearCache}>
                Vaciar la caché
              </Button>
            </div>
          )}
          {advanced && (
            <div className="grid gap-4 sm:grid-cols-2">
              <Field
                label="Directorio raíz"
                value={form.root_dir}
                onChange={(v) => set('root_dir', v)}
                placeholder="(raíz del repo)"
                hint="Para monorepos, p. ej. apps/web."
                mono
              />
              <Field
                label="Imagen de build"
                value={form.build_image}
                onChange={(v) => set('build_image', v)}
                placeholder={DEFAULT_IMAGE}
                hint="Usa node:22 con npm ci / npm run build si tu proyecto no funciona con Bun."
                mono
              />
            </div>
          )}

          <div className="space-y-2">
            <Label>Variables de entorno del build</Label>
            <p className="text-xs text-muted-foreground">
              Disponibles durante el build (p. ej. <code className="font-mono">VITE_API_URL</code>). Se guardan cifradas.
            </p>
            {env.map((row, i) => (
              <div key={i} className="flex gap-2">
                <Input
                  className="font-mono"
                  placeholder="NOMBRE"
                  value={row.key}
                  onChange={(e) =>
                    editEnv(env.map((r, j) => (j === i ? { ...r, key: e.target.value.replace(/[^A-Za-z0-9_]/g, '_') } : r)))
                  }
                />
                <Input
                  className="font-mono"
                  placeholder="valor"
                  value={row.value}
                  onChange={(e) => editEnv(env.map((r, j) => (j === i ? { ...r, value: e.target.value } : r)))}
                />
                <Button type="button" variant="ghost" size="icon" onClick={() => editEnv(env.filter((_, j) => j !== i))}>
                  <X />
                </Button>
              </div>
            ))}
            <Button type="button" variant="outline" size="sm" onClick={() => editEnv([...env, { key: '', value: '' }])}>
              <Plus /> Añadir variable
            </Button>
          </div>

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
            <Button type="submit" disabled={busy || !form.repo || !form.branch}>
              {busy && <Loader2 className="animate-spin" />}
              {linked ? 'Guardar' : 'Conectar'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

interface FieldProps {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  hint?: string;
  mono?: boolean;
}

function Field({ label, value, onChange, placeholder, hint, mono }: FieldProps) {
  return (
    <div className="space-y-2">
      <Label>{label}</Label>
      <Input value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} className={cn(mono && 'font-mono text-sm')} />
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}
