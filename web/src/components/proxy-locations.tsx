import { useEffect, useState } from 'react';
import { ChevronDown, ChevronRight, Plus, Trash2 } from 'lucide-react';
import { Trans, useLingui } from '@lingui/react/macro';

import { CheckField } from '@/components/check-field';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { api, type AccessList, type ProxyLocation, type Scheme, type TargetType } from '@/lib/api';

function newLocation(): ProxyLocation {
  return {
    path: '',
    target_type: 'docker',
    forward_scheme: 'http',
    forward_host: '',
    forward_port: 80,
    strip_prefix: false,
    access: 'inherit',
    access_list_id: null,
  };
}

/** Ubicaciones personalizadas de un proxy host (como en Nginx Proxy Manager). */
export function ProxyLocations({
  value,
  onChange,
  containers,
}: {
  value: ProxyLocation[];
  onChange: (locations: ProxyLocation[]) => void;
  /** Nombres de los contenedores, para sugerirlos. */
  containers: string[];
}) {
  // La ubicación desplegada (una a la vez); las nuevas se abren solas.
  const [open, setOpen] = useState<number | null>(null);
  const [lists, setLists] = useState<AccessList[]>([]);
  useEffect(() => {
    api
      .listAccessLists()
      .then(setLists)
      .catch(() => {});
  }, []);

  const update = (i: number, patch: Partial<ProxyLocation>) =>
    onChange(value.map((l, j) => (j === i ? { ...l, ...patch } : l)));

  function add() {
    onChange([...value, newLocation()]);
    setOpen(value.length);
  }

  return (
    <div className="space-y-3">
      <div className="flex items-start justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          <Trans>
            Send part of the domain to another target: for example <code className="font-mono">/api</code> to your API
            container. The rest of the domain keeps going to the main target.
          </Trans>
        </p>
        <Button type="button" variant="outline" size="sm" onClick={add} className="shrink-0">
          <Plus /> <Trans>Add location</Trans>
        </Button>
      </div>

      {value.length === 0 && (
        <p className="rounded-lg border border-dashed py-6 text-center text-sm text-muted-foreground">
          <Trans>No custom locations.</Trans>
        </p>
      )}

      {value.map((l, i) => (
        <LocationCard
          key={i}
          location={l}
          index={i}
          expanded={open === i}
          onToggle={() => setOpen(open === i ? null : i)}
          onChange={(patch) => update(i, patch)}
          onRemove={() => onChange(value.filter((_, j) => j !== i))}
          lists={lists}
          containers={containers}
        />
      ))}
    </div>
  );
}

function LocationCard({
  location: l,
  index,
  expanded,
  onToggle,
  onChange,
  onRemove,
  lists,
  containers,
}: {
  location: ProxyLocation;
  index: number;
  expanded: boolean;
  onToggle: () => void;
  onChange: (patch: Partial<ProxyLocation>) => void;
  onRemove: () => void;
  lists: AccessList[];
  containers: string[];
}) {
  const { t } = useLingui();
  const id = `loc-${index}`;
  const target = l.forward_host ? `${l.forward_scheme}://${l.forward_host}:${l.forward_port}` : t`no target yet`;
  const accessValue = l.access === 'list' && l.access_list_id ? String(l.access_list_id) : l.access;

  function setTarget(type: TargetType) {
    onChange({ target_type: type, forward_host: type === 'host' ? 'host.docker.internal' : '' });
  }

  return (
    <div className="rounded-lg border">
      <div className="flex items-center gap-2 px-3 py-2">
        <button
          type="button"
          onClick={onToggle}
          className="flex min-w-0 flex-1 items-center gap-2 text-left"
          aria-expanded={expanded}
        >
          {expanded ? <ChevronDown className="size-4 shrink-0" /> : <ChevronRight className="size-4 shrink-0" />}
          <span className="font-mono text-sm font-medium">{l.path || '/…'}</span>
          <span className="truncate font-mono text-xs text-muted-foreground">{target}</span>
        </button>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          className="text-destructive hover:text-destructive"
          onClick={onRemove}
          aria-label={t`Remove location ${l.path}`}
        >
          <Trash2 />
        </Button>
      </div>

      {/* Plegada no se pinta: sus campos obligatorios ocultos bloquearían el envío. */}
      {expanded && (
        <div className="space-y-4 border-t px-3 py-3">
          <div className="grid gap-3 sm:grid-cols-[1fr_11rem]">
            <div className="space-y-2">
              <Label htmlFor={`${id}-path`}>
                <Trans>Location</Trans>
              </Label>
              <Input
                id={`${id}-path`}
                value={l.path}
                onChange={(e) => onChange({ path: e.target.value.replace(/\s/g, '') })}
                placeholder="/api"
                className="font-mono"
                required
              />
            </div>
            <div className="space-y-2">
              <Label>
                <Trans>Target</Trans>
              </Label>
              <Select value={l.target_type} onValueChange={(v) => setTarget(v as TargetType)}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="docker">
                    <Trans>Container</Trans>
                  </SelectItem>
                  <SelectItem value="host">
                    <Trans>This server</Trans>
                  </SelectItem>
                  <SelectItem value="external">
                    <Trans>External</Trans>
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="grid grid-cols-[6.5rem_1fr_6.5rem] gap-3 max-sm:grid-cols-1">
            <div className="space-y-2">
              <Label>
                <Trans>Scheme</Trans>
              </Label>
              <Select value={l.forward_scheme} onValueChange={(v) => onChange({ forward_scheme: v as Scheme })}>
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
              <Label htmlFor={`${id}-host`}>
                {l.target_type === 'docker' ? <Trans>Container</Trans> : <Trans>Forward hostname / IP</Trans>}
              </Label>
              <Input
                id={`${id}-host`}
                value={l.forward_host}
                onChange={(e) => onChange({ forward_host: e.target.value.trim() })}
                placeholder={l.target_type === 'docker' ? t`container-name` : t`192.168.1.50 or api.other.com`}
                list={l.target_type === 'docker' ? `${id}-containers` : undefined}
                required
              />
              {l.target_type === 'docker' && (
                <datalist id={`${id}-containers`}>
                  {containers.map((c) => (
                    <option key={c} value={c} />
                  ))}
                </datalist>
              )}
            </div>
            <div className="space-y-2">
              <Label htmlFor={`${id}-port`}>
                <Trans>Port</Trans>
              </Label>
              <Input
                id={`${id}-port`}
                type="number"
                min={1}
                max={65535}
                value={l.forward_port}
                onChange={(e) => onChange({ forward_port: Number(e.target.value) })}
                required
              />
            </div>
          </div>

          <div className="space-y-2">
            <Label>
              <Trans>Access list</Trans>
            </Label>
            <Select
              value={accessValue}
              onValueChange={(v) =>
                v === 'inherit' || v === 'public'
                  ? onChange({ access: v, access_list_id: null })
                  : onChange({ access: 'list', access_list_id: Number(v) })
              }
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="inherit">
                  <Trans>Inherit from proxy host</Trans>
                </SelectItem>
                <SelectItem value="public">
                  <Trans>Public</Trans>
                </SelectItem>
                {lists.map((list) => (
                  <SelectItem key={list.id} value={String(list.id)}>
                    {list.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <CheckField
            id={`${id}-strip`}
            checked={l.strip_prefix}
            onChange={(v) => onChange({ strip_prefix: v })}
            label={t`Strip the location from the path`}
            hint={
              l.path
                ? t`The target receives ${l.path}/users as /users. Leave it off if the target expects the full path.`
                : t`The target receives the path without the location. Leave it off if the target expects the full path.`
            }
          />
        </div>
      )}
    </div>
  );
}
