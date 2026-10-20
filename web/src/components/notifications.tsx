import { useCallback, useEffect, useState } from 'react';
import { Bell, Loader2, Mail, Pencil, Plus, Send, Trash2, TriangleAlert, Webhook } from 'lucide-react';
import { toast } from 'sonner';

import { CheckField } from '@/components/check-field';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import {
  api,
  errorMessage,
  SECRET_MASK,
  type ChannelInput,
  type ChannelType,
  type NotificationChannel,
  type NotificationEvent,
} from '@/lib/api';
import { timeAgo } from '@/lib/format';

const eventLabels: Record<NotificationEvent, string> = {
  build_failed: 'Build fallido',
  build_succeeded: 'Sitio publicado',
  cert_error: 'Error al obtener un certificado SSL',
  cert_expiring: 'Certificado SSL a punto de caducar',
  container_down: 'Contenedor caído',
  container_recovered: 'Contenedor recuperado',
  backup_failed: 'Copia de una base de datos fallida',
};

const typeLabels: Record<ChannelType, string> = {
  email: 'Email (SMTP)',
  discord: 'Discord',
  slack: 'Slack',
  telegram: 'Telegram',
  webhook: 'Webhook',
};

type FieldDef = { key: string; label: string; placeholder?: string; secret?: boolean; type?: string; hint?: string; optional?: boolean };

const fields: Record<ChannelType, FieldDef[]> = {
  discord: [
    {
      key: 'webhook_url',
      label: 'URL del webhook',
      placeholder: 'https://discord.com/api/webhooks/…',
      secret: true,
      hint: 'En Discord: Ajustes del canal → Integraciones → Webhooks → Nuevo webhook → Copiar URL.',
    },
  ],
  slack: [
    {
      key: 'webhook_url',
      label: 'URL del webhook',
      placeholder: 'https://hooks.slack.com/services/…',
      secret: true,
      hint: 'Crea una app en api.slack.com con «Incoming Webhooks» y añade un webhook al canal.',
    },
  ],
  telegram: [
    { key: 'bot_token', label: 'Token del bot', placeholder: '123456:ABC-DEF…', secret: true, hint: 'Créalo hablando con @BotFather.' },
    {
      key: 'chat_id',
      label: 'Chat ID',
      placeholder: '123456789 o -100…',
      hint: 'Escríbele al bot y obtén tu ID con @userinfobot; para un grupo, añade el bot y usa el ID del grupo (empieza por -100).',
    },
  ],
  webhook: [
    { key: 'url', label: 'URL', placeholder: 'https://mi-servicio.com/dockfly' },
    {
      key: 'secret',
      label: 'Secreto',
      secret: true,
      optional: true,
      hint: 'Opcional: Dockfly firmará el cuerpo con HMAC-SHA256 en la cabecera X-Dockfly-Signature.',
    },
  ],
  email: [
    { key: 'host', label: 'Servidor SMTP', placeholder: 'smtp.gmail.com' },
    { key: 'port', label: 'Puerto', placeholder: '587', type: 'number' },
    { key: 'username', label: 'Usuario', optional: true },
    { key: 'password', label: 'Contraseña', secret: true, optional: true, hint: 'En Gmail, usa una contraseña de aplicación.' },
    { key: 'from', label: 'Remitente', placeholder: 'dockfly@midominio.com' },
    { key: 'to', label: 'Destinatarios', placeholder: 'yo@midominio.com, equipo@midominio.com' },
  ],
};

function ChannelIcon({ type }: { type: ChannelType }) {
  if (type === 'email') return <Mail className="size-4" />;
  if (type === 'webhook') return <Webhook className="size-4" />;
  if (type === 'telegram') return <Send className="size-4" />;
  return <Bell className="size-4" />;
}

export function Notifications() {
  const [channels, setChannels] = useState<NotificationChannel[] | null>(null);
  const [editing, setEditing] = useState<NotificationChannel | 'new' | null>(null);
  const [testing, setTesting] = useState<number | null>(null);

  const load = useCallback(() => {
    api.listChannels().then(setChannels).catch((e) => toast.error(errorMessage(e)));
  }, []);
  useEffect(load, [load]);

  async function test(c: NotificationChannel) {
    setTesting(c.id);
    try {
      await api.testChannel(c.id);
      toast.success(`Aviso de prueba enviado a ${c.name}`);
    } catch (e) {
      toast.error(`${c.name} no funciona`, { description: errorMessage(e), duration: 10_000 });
    } finally {
      setTesting(null);
      load();
    }
  }

  async function toggle(c: NotificationChannel) {
    try {
      await api.updateChannel(c.id, { type: c.type, name: c.name, events: c.events, enabled: !c.enabled, config: c.config });
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  async function remove(c: NotificationChannel) {
    try {
      await api.deleteChannel(c.id);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">Notificaciones</CardTitle>
        <CardDescription>
          Avisos cuando falla un build, un certificado SSL da problemas o cae un contenedor. Cada aviso se envía una sola
          vez, no en bucle.
        </CardDescription>
        <CardAction>
          <Button size="sm" onClick={() => setEditing('new')}>
            <Plus /> Añadir canal
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        {channels && channels.length === 0 && (
          <p className="py-4 text-center text-sm text-muted-foreground">Todavía no hay canales de notificación.</p>
        )}
        {channels && channels.length > 0 && (
          <ul className="divide-y rounded-lg border">
            {channels.map((c) => (
              <li key={c.id} className="flex flex-wrap items-center gap-3 px-4 py-3">
                <ChannelIcon type={c.type} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2 font-medium">
                    {c.name}
                    <span className="text-xs font-normal text-muted-foreground">{typeLabels[c.type]}</span>
                  </div>
                  <div className="mt-1 flex flex-wrap gap-1">
                    {c.events.map((e) => (
                      <Badge key={e} variant="outline" className="font-normal">
                        {eventLabels[e]}
                      </Badge>
                    ))}
                  </div>
                  {c.last_error ? (
                    <p className="mt-1 flex items-center gap-1 text-xs text-destructive">
                      <TriangleAlert className="size-3" /> Último envío fallido: {c.last_error}
                    </p>
                  ) : (
                    c.last_sent_at && <p className="mt-1 text-xs text-muted-foreground">Último aviso {timeAgo(c.last_sent_at)}</p>
                  )}
                </div>
                <Button variant="outline" size="sm" onClick={() => test(c)} disabled={testing === c.id}>
                  {testing === c.id ? <Loader2 className="animate-spin" /> : <Send />} Probar
                </Button>
                <Switch checked={c.enabled} onCheckedChange={() => toggle(c)} aria-label="Activo" />
                <Button variant="ghost" size="icon-sm" onClick={() => setEditing(c)} aria-label="Editar">
                  <Pencil />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-destructive hover:text-destructive"
                  onClick={() => remove(c)}
                  aria-label="Eliminar"
                >
                  <Trash2 />
                </Button>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
      {editing && (
        <ChannelDialog
          channel={editing === 'new' ? undefined : editing}
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

function ChannelDialog({ channel, onClose, onSaved }: { channel?: NotificationChannel; onClose: () => void; onSaved: () => void }) {
  const [type, setType] = useState<ChannelType>(channel?.type ?? 'discord');
  const [name, setName] = useState(channel?.name ?? '');
  const [events, setEvents] = useState<NotificationEvent[]>(
    channel?.events ?? ['build_failed', 'cert_error', 'cert_expiring', 'container_down', 'container_recovered', 'backup_failed'],
  );
  // Los campos se editan como texto; "to" (lista) y "port" (número) se convierten al guardar.
  const [config, setConfig] = useState<Record<string, string>>(() => {
    const c = channel?.config ?? {};
    return Object.fromEntries(
      Object.entries(c).map(([k, v]) => [k, Array.isArray(v) ? v.join(', ') : String(v ?? '')]),
    );
  });
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  function changeType(t: ChannelType) {
    setType(t);
    setConfig(t === 'email' ? { port: '587', security: 'starttls' } : {});
  }

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    const cfg: Record<string, unknown> = { ...config };
    if (type === 'email') {
      cfg.port = Number(config.port);
      cfg.to = (config.to ?? '').split(/[\s,;]+/).filter(Boolean);
    }
    const input: ChannelInput = { type, name, events, enabled: channel?.enabled ?? true, config: cfg };
    try {
      if (channel) await api.updateChannel(channel.id, input);
      else await api.createChannel(input);
      toast.success(channel ? 'Canal guardado' : 'Canal creado: pulsa «Probar» para comprobarlo');
      onSaved();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-lg">
        <form onSubmit={save} className="contents">
          <DialogHeader>
            <DialogTitle>{channel ? `Editar ${channel.name}` : 'Nuevo canal de notificación'}</DialogTitle>
            <DialogDescription>Los datos se guardan cifrados; los campos secretos no se vuelven a mostrar.</DialogDescription>
          </DialogHeader>

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label>Tipo</Label>
              <Select value={type} onValueChange={(v) => changeType(v as ChannelType)} disabled={!!channel}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {(Object.keys(typeLabels) as ChannelType[]).map((t) => (
                    <SelectItem key={t} value={t}>
                      {typeLabels[t]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="ch-name">Nombre</Label>
              <Input id="ch-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Equipo" required />
            </div>
          </div>

          {type === 'email' && (
            <div className="space-y-2">
              <Label>Seguridad</Label>
              <Select value={config.security ?? 'starttls'} onValueChange={(v) => setConfig((c) => ({ ...c, security: v }))}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="starttls">STARTTLS (puerto 587)</SelectItem>
                  <SelectItem value="tls">TLS (puerto 465)</SelectItem>
                  <SelectItem value="none">Sin cifrar (solo servidores locales)</SelectItem>
                </SelectContent>
              </Select>
            </div>
          )}

          <div className="grid gap-4 sm:grid-cols-2">
            {fields[type].map((f) => (
              <div key={f.key} className={f.hint || f.key.endsWith('url') || f.key === 'to' ? 'space-y-2 sm:col-span-2' : 'space-y-2'}>
                <Label htmlFor={`ch-${f.key}`}>
                  {f.label}
                  {f.optional && <span className="font-normal text-muted-foreground"> (opcional)</span>}
                </Label>
                <Input
                  id={`ch-${f.key}`}
                  type={f.secret ? 'password' : (f.type ?? 'text')}
                  value={config[f.key] ?? ''}
                  onChange={(e) => setConfig((c) => ({ ...c, [f.key]: e.target.value }))}
                  onFocus={(e) => f.secret && e.target.value === SECRET_MASK && setConfig((c) => ({ ...c, [f.key]: '' }))}
                  placeholder={f.placeholder}
                  required={!f.optional}
                  autoComplete="off"
                />
                {f.hint && <p className="text-xs text-muted-foreground">{f.hint}</p>}
              </div>
            ))}
          </div>

          <div className="space-y-2">
            <Label>Avisar de</Label>
            <div className="grid gap-2 sm:grid-cols-2">
              {(Object.keys(eventLabels) as NotificationEvent[]).map((ev) => (
                <CheckField
                  key={ev}
                  id={`ev-${ev}`}
                  checked={events.includes(ev)}
                  onChange={(v) => setEvents((list) => (v ? [...list, ev] : list.filter((x) => x !== ev)))}
                  label={eventLabels[ev]}
                />
              ))}
            </div>
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
            <Button type="submit" disabled={busy || events.length === 0}>
              {busy && <Loader2 className="animate-spin" />}
              Guardar
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
