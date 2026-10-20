import { useEffect, useState } from 'react';
import { Box, Globe, Info, Loader2, Server, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';

import { AccessSelect } from '@/components/access-select';
import { CheckField } from '@/components/check-field';
import { DomainsInput } from '@/components/domains-input';
import { SSLFields } from '@/components/ssl-fields';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import {
  api,
  errorMessage,
  type Container,
  type ProxyHost,
  type ProxyHostInput,
  type Scheme,
  type TargetType,
} from '@/lib/api';

// La opción activa debe distinguirse claramente: el estilo por defecto es muy sutil.
const segmentClass = 'flex-1 data-[state=on]:bg-primary data-[state=on]:text-primary-foreground';

/**
 * Docker muestra "sha256:…" cuando la etiqueta de la imagen se movió a otra
 * versión (p. ej. tras un pull) o se borró: lo acortamos.
 */
function imageLabel(image: string) {
  return image.startsWith('sha256:') ? `imagen sin etiqueta (${image.slice(7, 14)})` : image;
}

interface Props {
  /** Host a editar, o un objeto vacío para crear uno nuevo. */
  host: Partial<ProxyHost>;
  dockerAvailable: boolean;
  onClose: () => void;
  onSaved: () => void;
}

function initialForm(h: Partial<ProxyHost>, dockerAvailable: boolean): ProxyHostInput {
  return {
    domains: [...(h.domains ?? [])],
    target_type: h.target_type ?? (dockerAvailable ? 'docker' : 'external'),
    forward_scheme: h.forward_scheme ?? 'http',
    forward_host: h.forward_host ?? '',
    forward_port: h.forward_port ?? 80,
    ssl: h.ssl ?? false,
    force_https: h.force_https ?? true,
    hsts: h.hsts ?? false,
    skip_tls_verify: h.skip_tls_verify ?? false,
    preserve_host: h.preserve_host ?? true,
    enabled: h.enabled ?? true,
    access_list_id: h.access_list_id ?? null,
  };
}

export function ProxyHostDialog({ host, dockerAvailable, onClose, onSaved }: Props) {
  const isNew = host.id === undefined;
  // El componente se monta en cada apertura, así que el estado inicial basta.
  const [form, setForm] = useState(() => initialForm(host, dockerAvailable));
  const [error, setError] = useState('');
  const [warnings, setWarnings] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);

  const [containers, setContainers] = useState<Container[]>([]);
  const [selfNetworks, setSelfNetworks] = useState<string[]>([]);
  const [containersLoaded, setContainersLoaded] = useState(false);

  const set = <K extends keyof ProxyHostInput>(key: K, value: ProxyHostInput[K]) =>
    setForm((f) => ({ ...f, [key]: value }));

  useEffect(() => {
    if (form.target_type !== 'docker' || containersLoaded || !dockerAvailable) return;
    api
      .listContainers()
      .then((res) => {
        setContainers(res.containers);
        setSelfNetworks(res.self_networks ?? []);
      })
      .catch((e) => setError(errorMessage(e)))
      .finally(() => setContainersLoaded(true));
  }, [form.target_type, containersLoaded, dockerAvailable]);

  const selected = containers.find((c) => c.name === form.forward_host);
  const needsNetwork =
    form.target_type === 'docker' &&
    selected !== undefined &&
    selfNetworks.length > 0 &&
    !selected.networks.some((n) => selfNetworks.includes(n));

  function setTarget(type: TargetType) {
    setForm((f) =>
      f.target_type === type
        ? f
        : {
            ...f,
            target_type: type,
            forward_host: type === 'host' ? 'host.docker.internal' : '',
            // Los servicios externos (CDN, SaaS) suelen exigir su propio Host.
            preserve_host: type !== 'external',
          },
    );
  }

  function pickContainer(name: string) {
    const c = containers.find((c) => c.name === name);
    setForm((f) => ({ ...f, forward_host: name, forward_port: c?.ports[0] ?? f.forward_port }));
  }

  async function save(e: React.FormEvent) {
    e.preventDefault();
    const payload = form;
    setError('');
    setBusy(true);
    try {
      const res = isNew ? await api.createProxyHost(payload) : await api.updateProxyHost(host.id!, payload);
      if (res.warnings?.length) {
        setWarnings(res.warnings);
      } else {
        toast.success(isNew ? 'Proxy host creado' : 'Cambios guardados');
        onSaved();
      }
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <form onSubmit={save} className="contents">
          <DialogHeader>
            <DialogTitle>{isNew ? 'Nuevo proxy host' : 'Editar proxy host'}</DialogTitle>
            <DialogDescription>Traefik aplica los cambios al instante, sin reinicios.</DialogDescription>
          </DialogHeader>

          <Tabs defaultValue="details">
            <TabsList className="w-full">
              <TabsTrigger value="details">Detalles</TabsTrigger>
              <TabsTrigger value="ssl">SSL y acceso</TabsTrigger>
            </TabsList>

            <TabsContent value="details" className="space-y-5 pt-3">
              <div className="space-y-2">
                <Label htmlFor="domain-input">Dominios</Label>
                <DomainsInput id="domain-input" value={form.domains} onChange={(d) => set('domains', d)} />
              </div>

              <div className="space-y-2">
                <Label>Destino</Label>
                <ToggleGroup
                  type="single"
                  variant="outline"
                  spacing={0}
                  className="w-full"
                  value={form.target_type}
                  onValueChange={(v) => v && setTarget(v as TargetType)}
                >
                  <ToggleGroupItem value="docker" className={segmentClass}>
                    <Box /> Contenedor
                  </ToggleGroupItem>
                  <ToggleGroupItem value="host" className={segmentClass}>
                    <Server /> Este servidor
                  </ToggleGroupItem>
                  <ToggleGroupItem value="external" className={segmentClass}>
                    <Globe /> Externo
                  </ToggleGroupItem>
                </ToggleGroup>
              </div>

              <div className="grid grid-cols-[6.5rem_1fr_6.5rem] gap-3 max-sm:grid-cols-1">
                <div className="space-y-2">
                  <Label>Esquema</Label>
                  <Select value={form.forward_scheme} onValueChange={(v) => set('forward_scheme', v as Scheme)}>
                    <SelectTrigger className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="http">http</SelectItem>
                      <SelectItem value="https">https</SelectItem>
                    </SelectContent>
                  </Select>
                </div>

                <div className="min-w-0 space-y-2">
                  {form.target_type === 'docker' && dockerAvailable ? (
                    <>
                      <Label>Contenedor</Label>
                      <Select value={form.forward_host} onValueChange={pickContainer}>
                        <SelectTrigger className="w-full">
                          <SelectValue placeholder={containersLoaded ? 'Elige un contenedor…' : 'Cargando…'} />
                        </SelectTrigger>
                        {/* popper + ancho máximo: el desplegable no se sale del diálogo. */}
                        <SelectContent position="popper" className="max-w-[min(30rem,calc(100vw-2rem))]">
                          {containers.map((c) => (
                            <SelectItem key={c.id} value={c.name}>
                              <span className="shrink-0">{c.name}</span>
                              {c.self && (
                                <Badge variant="secondary" className="shrink-0">
                                  este panel
                                </Badge>
                              )}
                              <span className="min-w-0 truncate text-xs text-muted-foreground">
                                {imageLabel(c.image)}
                                {c.state !== 'running' && ` · ${c.state}`}
                              </span>
                            </SelectItem>
                          ))}
                          {form.forward_host && !selected && containersLoaded && (
                            <SelectItem value={form.forward_host}>{form.forward_host} (no encontrado)</SelectItem>
                          )}
                        </SelectContent>
                      </Select>
                    </>
                  ) : (
                    <>
                      <Label htmlFor="forward-host">
                        {form.target_type === 'docker' ? 'Contenedor' : form.target_type === 'host' ? 'Host' : 'IP o dominio'}
                      </Label>
                      <Input
                        id="forward-host"
                        value={form.forward_host}
                        onChange={(e) => set('forward_host', e.target.value)}
                        placeholder={form.target_type === 'docker' ? 'nombre-del-contenedor' : '192.168.1.50 o api.otro.com'}
                        required
                      />
                    </>
                  )}
                </div>

                <div className="space-y-2">
                  <Label htmlFor="forward-port">Puerto</Label>
                  <Input
                    id="forward-port"
                    type="number"
                    min={1}
                    max={65535}
                    value={form.forward_port}
                    onChange={(e) => set('forward_port', Number(e.target.value))}
                    list="container-ports"
                    required
                  />
                  {selected && selected.ports.length > 0 && (
                    <datalist id="container-ports">
                      {selected.ports.map((p) => (
                        <option key={p} value={p} />
                      ))}
                    </datalist>
                  )}
                </div>
              </div>

              {needsNetwork && (
                <Alert>
                  <Info />
                  <AlertDescription>
                    Este contenedor no está en la red de Dockfly. Al guardar se conectará automáticamente para que
                    Traefik pueda alcanzarlo.
                  </AlertDescription>
                </Alert>
              )}
              {form.target_type === 'host' && (
                <p className="text-xs leading-relaxed text-muted-foreground">
                  <code className="font-mono">host.docker.internal</code> apunta al servidor donde corre Dockfly. Útil
                  para apps fuera de Docker (systemd, PM2…). La app debe escuchar en una IP accesible desde Docker, no
                  solo en <code className="font-mono">127.0.0.1</code>.
                </p>
              )}

              <div className="space-y-3">
                {form.forward_scheme === 'https' && (
                  <CheckField
                    id="skip-tls"
                    checked={form.skip_tls_verify}
                    onChange={(v) => set('skip_tls_verify', v)}
                    label="No verificar el certificado del destino"
                    hint="Para destinos con certificados autofirmados."
                  />
                )}
                <CheckField
                  id="preserve-host"
                  checked={form.preserve_host}
                  onChange={(v) => set('preserve_host', v)}
                  label="Conservar el Host original"
                  hint="Desactívalo si el destino es un sitio externo que exige su propio dominio."
                />
                <CheckField id="enabled" checked={form.enabled} onChange={(v) => set('enabled', v)} label="Activo" />
              </div>
            </TabsContent>

            <TabsContent value="ssl" className="space-y-6 pt-3">
              <SSLFields value={form} onChange={set} />
              <AccessSelect value={form.access_list_id} onChange={(id) => set('access_list_id', id)} />
            </TabsContent>
          </Tabs>

          {error && (
            <Alert variant="destructive">
              <TriangleAlert />
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          {warnings.length > 0 && (
            <Alert>
              <TriangleAlert className="text-amber-500" />
              <AlertTitle>Guardado, con avisos</AlertTitle>
              <AlertDescription>
                <ul className="list-disc pl-4">
                  {warnings.map((w) => (
                    <li key={w}>{w}</li>
                  ))}
                </ul>
              </AlertDescription>
            </Alert>
          )}

          <DialogFooter>
            {warnings.length > 0 ? (
              <Button type="button" onClick={onSaved}>
                Entendido
              </Button>
            ) : (
              <>
                <Button type="button" variant="outline" onClick={onClose}>
                  Cancelar
                </Button>
                <Button type="submit" disabled={busy}>
                  {busy && <Loader2 className="animate-spin" />}
                  Guardar
                </Button>
              </>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
