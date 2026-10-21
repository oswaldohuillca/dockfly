import { useEffect, useState } from 'react';
import { Box, Globe, Info, Loader2, Server, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';
import { i18n } from '@lingui/core';
import { msg } from '@lingui/core/macro';
import { Trans, useLingui } from '@lingui/react/macro';

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
  if (!image.startsWith('sha256:')) return image;
  const digest = image.slice(7, 14);
  return i18n._(msg`untagged image (${digest})`);
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
  const { t } = useLingui();
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
        toast.success(isNew ? t`Proxy host created` : t`Changes saved`);
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
            <DialogTitle>{isNew ? <Trans>New proxy host</Trans> : <Trans>Edit proxy host</Trans>}</DialogTitle>
            <DialogDescription>
              <Trans>Traefik applies changes instantly, with no restarts.</Trans>
            </DialogDescription>
          </DialogHeader>

          <Tabs defaultValue="details">
            <TabsList className="w-full">
              <TabsTrigger value="details">
                <Trans>Details</Trans>
              </TabsTrigger>
              <TabsTrigger value="ssl">
                <Trans>SSL & access</Trans>
              </TabsTrigger>
            </TabsList>

            <TabsContent value="details" className="space-y-5 pt-3">
              <div className="space-y-2">
                <Label htmlFor="domain-input">
                  <Trans>Domains</Trans>
                </Label>
                <DomainsInput id="domain-input" value={form.domains} onChange={(d) => set('domains', d)} />
              </div>

              <div className="space-y-2">
                <Label>
                  <Trans>Target</Trans>
                </Label>
                <ToggleGroup
                  type="single"
                  variant="outline"
                  spacing={0}
                  className="w-full"
                  value={form.target_type}
                  onValueChange={(v) => v && setTarget(v as TargetType)}
                >
                  <ToggleGroupItem value="docker" className={segmentClass}>
                    <Box /> <Trans>Container</Trans>
                  </ToggleGroupItem>
                  <ToggleGroupItem value="host" className={segmentClass}>
                    <Server /> <Trans>This server</Trans>
                  </ToggleGroupItem>
                  <ToggleGroupItem value="external" className={segmentClass}>
                    <Globe /> <Trans>External</Trans>
                  </ToggleGroupItem>
                </ToggleGroup>
              </div>

              <div className="grid grid-cols-[6.5rem_1fr_6.5rem] gap-3 max-sm:grid-cols-1">
                <div className="space-y-2">
                  <Label>
                    <Trans>Scheme</Trans>
                  </Label>
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
                      <Label>
                        <Trans>Container</Trans>
                      </Label>
                      <Select value={form.forward_host} onValueChange={pickContainer}>
                        <SelectTrigger className="w-full">
                          <SelectValue placeholder={containersLoaded ? t`Choose a container…` : t`Loading…`} />
                        </SelectTrigger>
                        {/* popper + ancho máximo: el desplegable no se sale del diálogo. */}
                        <SelectContent position="popper" className="max-w-[min(30rem,calc(100vw-2rem))]">
                          {containers.map((c) => (
                            <SelectItem key={c.id} value={c.name}>
                              <span className="shrink-0">{c.name}</span>
                              {c.self && (
                                <Badge variant="secondary" className="shrink-0">
                                  <Trans>this panel</Trans>
                                </Badge>
                              )}
                              <span className="min-w-0 truncate text-xs text-muted-foreground">
                                {imageLabel(c.image)}
                                {c.state !== 'running' && ` · ${c.state}`}
                              </span>
                            </SelectItem>
                          ))}
                          {form.forward_host && !selected && containersLoaded && (
                            <SelectItem value={form.forward_host}>
                              <Trans>{form.forward_host} (not found)</Trans>
                            </SelectItem>
                          )}
                        </SelectContent>
                      </Select>
                    </>
                  ) : (
                    <>
                      <Label htmlFor="forward-host">
                        {form.target_type === 'docker' ? (
                          <Trans>Container</Trans>
                        ) : form.target_type === 'host' ? (
                          'Host'
                        ) : (
                          <Trans>IP or domain</Trans>
                        )}
                      </Label>
                      <Input
                        id="forward-host"
                        value={form.forward_host}
                        onChange={(e) => set('forward_host', e.target.value)}
                        placeholder={form.target_type === 'docker' ? t`container-name` : t`192.168.1.50 or api.other.com`}
                        required
                      />
                    </>
                  )}
                </div>

                <div className="space-y-2">
                  <Label htmlFor="forward-port">
                    <Trans>Port</Trans>
                  </Label>
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
                    <Trans>
                      This container isn't on the Dockfly network. Saving will connect it automatically so Traefik can
                      reach it.
                    </Trans>
                  </AlertDescription>
                </Alert>
              )}
              {form.target_type === 'host' && (
                <p className="text-xs leading-relaxed text-muted-foreground">
                  <Trans>
                    <code className="font-mono">host.docker.internal</code> points to the server Dockfly runs on. Useful
                    for apps outside Docker (systemd, PM2…). The app must listen on an IP reachable from Docker, not
                    just on <code className="font-mono">127.0.0.1</code>.
                  </Trans>
                </p>
              )}

              <div className="space-y-3">
                {form.forward_scheme === 'https' && (
                  <CheckField
                    id="skip-tls"
                    checked={form.skip_tls_verify}
                    onChange={(v) => set('skip_tls_verify', v)}
                    label={t`Don't verify the target's certificate`}
                    hint={t`For targets with self-signed certificates.`}
                  />
                )}
                <CheckField
                  id="preserve-host"
                  checked={form.preserve_host}
                  onChange={(v) => set('preserve_host', v)}
                  label={t`Preserve the original Host`}
                  hint={t`Turn it off if the target is an external site that requires its own domain.`}
                />
                <CheckField id="enabled" checked={form.enabled} onChange={(v) => set('enabled', v)} label={t`Enabled`} />
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
              <AlertTitle>
                <Trans>Saved, with warnings</Trans>
              </AlertTitle>
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
                <Trans>Got it</Trans>
              </Button>
            ) : (
              <>
                <Button type="button" variant="outline" onClick={onClose}>
                  <Trans>Cancel</Trans>
                </Button>
                <Button type="submit" disabled={busy}>
                  {busy && <Loader2 className="animate-spin" />}
                  <Trans>Save</Trans>
                </Button>
              </>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
