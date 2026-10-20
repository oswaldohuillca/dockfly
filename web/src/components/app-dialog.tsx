import { useCallback, useEffect, useState } from 'react';
import { Link } from '@tanstack/react-router';
import { Box, ChevronDown, GitBranch, Layers, Loader2, Plus, TriangleAlert, X } from 'lucide-react';
import { toast } from 'sonner';

import { AccessSelect } from '@/components/access-select';
import { CheckField } from '@/components/check-field';
import { DomainsInput } from '@/components/domains-input';
import { EnvEditor, envToRows, rowsToEnv, type EnvRow } from '@/components/env-editor';
import { RepoPicker } from '@/components/repo-picker';
import { SSLFields } from '@/components/ssl-fields';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { api, errorMessage, type App, type AppInput, type AppSource, type GitConnection, type RemoteServer } from '@/lib/api';
import { appTemplates } from '@/lib/app-templates';
import { cn } from '@/lib/utils';

interface Props {
  /** App a editar, o undefined para crear una nueva. */
  app?: App;
  onClose: () => void;
  onSaved: (app: App) => void;
}

const empty: AppInput = {
  name: '', source: 'image', image: '', connection_id: 0, repo: '', branch: '', root_dir: '', dockerfile: '',
  compose_file: '', compose_service: '',
  port: 0, domains: [], ssl: true, force_https: true, hsts: false, env: null, volumes: [], command: '',
  memory_mb: 0, cpus: 0, health_path: '', access_list_id: null,
};

export function AppDialog({ app, onClose, onSaved }: Props) {
  const isNew = app === undefined;
  const [form, setForm] = useState<AppInput>(() =>
    app
      ? {
          ...empty, ...app, connection_id: app.git_connection_id ?? 0, env: null,
          volumes: app.volumes.map((v) => ({ ...v })), domains: [...app.domains],
        }
      : empty,
  );
  const [env, setEnv] = useState<EnvRow[]>([]);
  const [envTouched, setEnvTouched] = useState(false);
  const [connections, setConnections] = useState<GitConnection[]>([]);
  const [servers, setServers] = useState<RemoteServer[]>([]);
  const [advanced, setAdvanced] = useState(!!app && (app.command !== '' || app.memory_mb > 0 || app.cpus > 0));
  const [deploy, setDeploy] = useState(true);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const set = <K extends keyof AppInput>(key: K, value: AppInput[K]) => setForm((f) => ({ ...f, [key]: value }));
  const onRepoError = useCallback((msg: string) => setError(msg), []);

  useEffect(() => {
    api.listConnections().then((list) => {
      setConnections(list);
      setForm((f) => (f.connection_id === 0 && list.length > 0 ? { ...f, connection_id: list[0].id } : f));
    });
    if (app) api.getAppEnv(app.id).then((e) => setEnv(envToRows(e)));
    else
      api
        .listServers()
        .then(setServers)
        .catch(() => {}); // solo los administradores ven los servidores
  }, [app]);

  function applyTemplate(id: string) {
    const t = appTemplates.find((t) => t.id === id)!.build();
    setForm((f) => ({ ...f, ...t, source: 'image', env: null }));
    setEnv(envToRows(t.env ?? {}));
    setEnvTouched(true);
    if (t.command || t.memory_mb) setAdvanced(true);
  }

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    const payload: AppInput = {
      ...form,
      env: envTouched || isNew ? rowsToEnv(env) : null,
      port: Number(form.port) || 0,
      memory_mb: Number(form.memory_mb) || 0,
      cpus: Number(form.cpus) || 0,
      deploy: isNew && deploy,
    };
    try {
      if (isNew) {
        const res = await api.createApp(payload);
        if (res.warning) toast.warning('App creada, con un aviso', { description: res.warning });
        else toast.success(deploy ? `Desplegando ${res.app.name}…` : `${res.app.name} creada`);
        onSaved(res.app);
      } else {
        const res = await api.updateApp(app.id, payload);
        toast.success('Cambios guardados', {
          description: 'Los dominios ya están aplicados; el resto se aplica en el próximo despliegue.',
        });
        if (res.warning) toast.warning(res.warning);
        onSaved(res.app);
      }
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const internalHost = form.name ? `${form.name}${form.port ? `:${form.port}` : ''}` : '';

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[92svh] overflow-y-auto sm:max-w-2xl">
        <form onSubmit={save} className="contents">
          <DialogHeader>
            <DialogTitle>{isNew ? 'Nueva app' : `Configurar ${app.name}`}</DialogTitle>
            <DialogDescription>
              Un contenedor que Dockfly gestiona de principio a fin: despliegues sin corte, variables cifradas, volúmenes y
              dominio con SSL.
            </DialogDescription>
          </DialogHeader>

          {isNew && (
            <div className="space-y-2">
              <Label>Empezar desde una plantilla</Label>
              <div className="flex flex-wrap gap-1.5">
                {appTemplates.map((t) => (
                  <Button key={t.id} type="button" variant="outline" size="sm" onClick={() => applyTemplate(t.id)} title={t.description}>
                    {t.label}
                  </Button>
                ))}
              </div>
            </div>
          )}

          <Tabs defaultValue="general">
            <TabsList className="w-full">
              <TabsTrigger value="general">General</TabsTrigger>
              <TabsTrigger value="env">Variables{env.length > 0 && ` (${env.length})`}</TabsTrigger>
              {form.source !== 'compose' && (
                <TabsTrigger value="volumes">Volúmenes{form.volumes.length > 0 && ` (${form.volumes.length})`}</TabsTrigger>
              )}
              <TabsTrigger value="ssl">Dominio y SSL</TabsTrigger>
            </TabsList>

            <TabsContent value="general" className="space-y-5 pt-3">
              {isNew && servers.length > 0 && (
                <div className="space-y-2">
                  <Label>Servidor</Label>
                  <Select
                    value={String(form.server_id ?? 0)}
                    onValueChange={(v) => set('server_id', v === '0' ? null : Number(v))}
                  >
                    <SelectTrigger className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="0">Este servidor (el del panel)</SelectItem>
                      {servers.map((s) => (
                        <SelectItem key={s.id} value={String(s.id)} disabled={!s.online}>
                          {s.name}
                          {!s.online && ' (desconectado)'}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <p className="text-xs text-muted-foreground">
                    {form.server_id
                      ? 'Sus dominios deben apuntar a la IP de ese servidor: su propio Traefik sirve el tráfico y pide los certificados. No se puede cambiar después.'
                      : 'No se puede cambiar después.'}
                  </p>
                </div>
              )}
              <div className="grid gap-4 sm:grid-cols-[1fr_8rem]">
                <div className="space-y-2">
                  <Label htmlFor="app-name">Nombre</Label>
                  <Input
                    id="app-name"
                    value={form.name}
                    onChange={(e) => set('name', e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-'))}
                    placeholder="mi-api"
                    disabled={!isNew}
                    required
                  />
                  <p className="text-xs text-muted-foreground">
                    {isNew ? 'No se puede cambiar después: de él dependen sus volúmenes. ' : ''}
                    {internalHost && (
                      <>
                        Otras apps la encuentran en <code className="font-mono">{internalHost}</code>.
                      </>
                    )}
                  </p>
                </div>
                <div className="space-y-2">
                  <Label htmlFor="app-port">Puerto</Label>
                  <Input
                    id="app-port"
                    type="number"
                    min={0}
                    max={65535}
                    value={form.port || ''}
                    onChange={(e) => set('port', Number(e.target.value))}
                    placeholder="3000"
                  />
                  <p className="text-xs text-muted-foreground">En el que escucha. Vacío si no es web.</p>
                </div>
              </div>

              <div className="space-y-2">
                <Label>Origen</Label>
                <ToggleGroup
                  type="single"
                  variant="outline"
                  spacing={0}
                  className="w-full"
                  value={form.source}
                  onValueChange={(v) => v && set('source', v as AppSource)}
                >
                  <ToggleGroupItem value="image" className="flex-1 data-[state=on]:bg-primary data-[state=on]:text-primary-foreground">
                    <Box /> Imagen de Docker
                  </ToggleGroupItem>
                  <ToggleGroupItem value="git" className="flex-1 data-[state=on]:bg-primary data-[state=on]:text-primary-foreground">
                    <GitBranch /> Dockerfile
                  </ToggleGroupItem>
                  <ToggleGroupItem value="compose" disabled={!!form.server_id} className="flex-1 data-[state=on]:bg-primary data-[state=on]:text-primary-foreground">
                    <Layers /> docker-compose.yml
                  </ToggleGroupItem>
                </ToggleGroup>
              </div>

              {form.source === 'image' ? (
                <div className="space-y-2">
                  <Label htmlFor="app-image">Imagen</Label>
                  <Input
                    id="app-image"
                    className="font-mono"
                    value={form.image}
                    onChange={(e) => set('image', e.target.value)}
                    placeholder="nginx:alpine, ghcr.io/usuario/app:1.0…"
                    required
                  />
                </div>
              ) : connections.length === 0 ? (
                <Alert>
                  <TriangleAlert />
                  <AlertDescription>
                    Vincula primero tu cuenta de GitHub o GitLab en{' '}
                    <Link to="/integrations" className="font-medium underline">
                      Integraciones
                    </Link>
                    .
                  </AlertDescription>
                </Alert>
              ) : (
                <>
                  <RepoPicker
                    connections={connections}
                    value={{ connection_id: form.connection_id, repo: form.repo, branch: form.branch }}
                    onChange={(v) => setForm((f) => ({ ...f, ...v }))}
                    onError={onRepoError}
                  />
                  <div className="grid gap-4 sm:grid-cols-2">
                    <div className="space-y-2">
                      <Label htmlFor="app-root">Directorio</Label>
                      <Input
                        id="app-root"
                        className="font-mono"
                        value={form.root_dir}
                        onChange={(e) => set('root_dir', e.target.value)}
                        placeholder="(raíz del repo)"
                      />
                    </div>
                    {form.source === 'git' ? (
                      <div className="space-y-2">
                        <Label htmlFor="app-dockerfile">Dockerfile</Label>
                        <Input
                          id="app-dockerfile"
                          className="font-mono"
                          value={form.dockerfile}
                          onChange={(e) => set('dockerfile', e.target.value)}
                          placeholder="Dockerfile"
                        />
                      </div>
                    ) : (
                      <div className="space-y-2">
                        <Label htmlFor="app-compose">Archivo de Compose</Label>
                        <Input
                          id="app-compose"
                          className="font-mono"
                          value={form.compose_file}
                          onChange={(e) => set('compose_file', e.target.value)}
                          placeholder="docker-compose.yml"
                        />
                      </div>
                    )}
                  </div>
                  {form.source === 'compose' && (
                    <div className="space-y-2">
                      <Label htmlFor="app-service">Servicio web</Label>
                      <Input
                        id="app-service"
                        className="font-mono"
                        value={form.compose_service}
                        onChange={(e) => set('compose_service', e.target.value)}
                        placeholder="web"
                      />
                      <p className="text-xs text-muted-foreground">
                        El servicio del docker-compose.yml que recibe el tráfico de los dominios (en el puerto indicado
                        arriba). Los volúmenes se definen en el propio archivo; las rutas relativas del host
                        (<code className="font-mono">./datos:/datos</code>) no funcionan: usa volúmenes con nombre.
                      </p>
                    </div>
                  )}
                </>
              )}

              <button
                type="button"
                onClick={() => setAdvanced((a) => !a)}
                className="flex w-fit items-center gap-1 text-sm font-medium text-muted-foreground hover:text-foreground"
              >
                <ChevronDown className={cn('size-4 transition-transform', advanced && 'rotate-180')} /> Opciones avanzadas
              </button>
              {advanced && (
                <div className="grid gap-4 sm:grid-cols-2">
                  <div className="space-y-2 sm:col-span-2">
                    <Label htmlFor="app-cmd">Comando</Label>
                    <Input
                      id="app-cmd"
                      className="font-mono"
                      value={form.command}
                      onChange={(e) => set('command', e.target.value)}
                      placeholder="(el de la imagen)"
                    />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="app-health">Ruta de salud</Label>
                    <Input
                      id="app-health"
                      className="font-mono"
                      value={form.health_path}
                      onChange={(e) => set('health_path', e.target.value)}
                      placeholder="/health"
                    />
                    <p className="text-xs text-muted-foreground">
                      Un despliegue no recibe tráfico hasta que esta ruta responde. Sin ella, basta con que el puerto acepte
                      conexiones.
                    </p>
                  </div>
                  <div className="grid grid-cols-2 gap-3">
                    <div className="space-y-2">
                      <Label htmlFor="app-mem">Memoria (MB)</Label>
                      <Input id="app-mem" type="number" min={0} value={form.memory_mb || ''} onChange={(e) => set('memory_mb', Number(e.target.value))} placeholder="sin límite" />
                    </div>
                    <div className="space-y-2">
                      <Label htmlFor="app-cpu">CPUs</Label>
                      <Input id="app-cpu" type="number" min={0} step={0.25} value={form.cpus || ''} onChange={(e) => set('cpus', Number(e.target.value))} placeholder="sin límite" />
                    </div>
                  </div>
                </div>
              )}
            </TabsContent>

            <TabsContent value="env" className="space-y-2 pt-3">
              <p className="text-xs text-muted-foreground">Se guardan cifradas y se aplican en el próximo despliegue.</p>
              <EnvEditor
                rows={env}
                onChange={(rows) => {
                  setEnv(rows);
                  setEnvTouched(true);
                }}
              />
            </TabsContent>

            <TabsContent value="volumes" className="space-y-3 pt-3">
              <p className="text-xs text-muted-foreground">
                Los datos que deben sobrevivir a los despliegues (bases de datos, subidas…). Con volúmenes, cada despliegue
                detiene la versión actual antes de arrancar la nueva: dos instancias escribiendo en los mismos datos los
                corromperían.
              </p>
              {form.volumes.map((v, i) => (
                <div key={i} className="flex gap-2">
                  <Input
                    className="w-40 font-mono"
                    placeholder="data"
                    value={v.name}
                    onChange={(e) =>
                      set('volumes', form.volumes.map((x, j) => (j === i ? { ...x, name: e.target.value.toLowerCase() } : x)))
                    }
                  />
                  <Input
                    className="font-mono"
                    placeholder="/var/lib/datos"
                    value={v.path}
                    onChange={(e) => set('volumes', form.volumes.map((x, j) => (j === i ? { ...x, path: e.target.value } : x)))}
                  />
                  <Button type="button" variant="ghost" size="icon" onClick={() => set('volumes', form.volumes.filter((_, j) => j !== i))}>
                    <X />
                  </Button>
                </div>
              ))}
              <Button type="button" variant="outline" size="sm" onClick={() => set('volumes', [...form.volumes, { name: '', path: '' }])}>
                <Plus /> Añadir volumen
              </Button>
            </TabsContent>

            <TabsContent value="ssl" className="space-y-4 pt-3">
              <div className="space-y-2">
                <Label htmlFor="app-domains">Dominios</Label>
                <DomainsInput id="app-domains" value={form.domains} onChange={(d) => set('domains', d)} />
                {!form.port && form.domains.length > 0 && (
                  <p className="text-xs text-destructive">Indica el puerto de la app (pestaña General) para publicarla.</p>
                )}
              </div>
              <SSLFields value={form} onChange={set} />
              <AccessSelect value={form.access_list_id} onChange={(id) => set('access_list_id', id)} />
            </TabsContent>
          </Tabs>

          {isNew && <CheckField id="app-deploy" checked={deploy} onChange={setDeploy} label="Desplegar al crearla" />}
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
              {busy && <Loader2 className="animate-spin" />}
              {isNew ? 'Crear app' : 'Guardar'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
