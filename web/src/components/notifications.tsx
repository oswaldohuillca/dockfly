import { useCallback, useEffect, useState } from 'react';
import { Bell, Loader2, Mail, Pencil, Plus, Send, Trash2, TriangleAlert, Webhook } from 'lucide-react';
import { toast } from 'sonner';
import { msg } from '@lingui/core/macro';
import type { MessageDescriptor } from '@lingui/core';
import { useLingui } from '@lingui/react';
import { Trans, useLingui as useLinguiMacro } from '@lingui/react/macro';

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

const eventLabels: Record<NotificationEvent, MessageDescriptor> = {
  build_failed: msg`Build failed`,
  build_succeeded: msg`Site published`,
  cert_error: msg`Error obtaining an SSL certificate`,
  cert_expiring: msg`SSL certificate about to expire`,
  container_down: msg`Container down`,
  container_recovered: msg`Container recovered`,
  backup_failed: msg`Database backup failed`,
  server_down: msg`Server disconnected`,
  server_up: msg`Server reconnected`,
};

const typeLabels: Record<ChannelType, string> = {
  email: 'Email (SMTP)',
  discord: 'Discord',
  slack: 'Slack',
  telegram: 'Telegram',
  webhook: 'Webhook',
};

type FieldDef = { key: string; label: MessageDescriptor | string; placeholder?: MessageDescriptor | string; secret?: boolean; type?: string; hint?: MessageDescriptor; optional?: boolean };

const fields: Record<ChannelType, FieldDef[]> = {
  discord: [
    {
      key: 'webhook_url',
      label: msg`Webhook URL`,
      placeholder: 'https://discord.com/api/webhooks/…',
      secret: true,
      hint: msg`In Discord: Channel settings → Integrations → Webhooks → New webhook → Copy webhook URL.`,
    },
  ],
  slack: [
    {
      key: 'webhook_url',
      label: msg`Webhook URL`,
      placeholder: 'https://hooks.slack.com/services/…',
      secret: true,
      hint: msg`Create an app at api.slack.com with "Incoming Webhooks" and add a webhook to the channel.`,
    },
  ],
  telegram: [
    { key: 'bot_token', label: msg`Bot token`, placeholder: '123456:ABC-DEF…', secret: true, hint: msg`Create it by talking to @BotFather.` },
    {
      key: 'chat_id',
      label: 'Chat ID',
      placeholder: msg`123456789 or -100…`,
      hint: msg`Message the bot and get your ID with @userinfobot; for a group, add the bot and use the group ID (it starts with -100).`,
    },
  ],
  webhook: [
    { key: 'url', label: 'URL', placeholder: msg`https://my-service.com/dockfly` },
    {
      key: 'secret',
      label: msg`Secret`,
      secret: true,
      optional: true,
      hint: msg`Optional: Dockfly will sign the body with HMAC-SHA256 in the X-Dockfly-Signature header.`,
    },
  ],
  email: [
    { key: 'host', label: msg`SMTP server`, placeholder: 'smtp.gmail.com' },
    { key: 'port', label: msg`Port`, placeholder: '587', type: 'number' },
    { key: 'username', label: msg`Username`, optional: true },
    { key: 'password', label: msg`Password`, secret: true, optional: true, hint: msg`For Gmail, use an app password.` },
    { key: 'from', label: msg`Sender`, placeholder: msg`dockfly@mydomain.com` },
    { key: 'to', label: msg`Recipients`, placeholder: msg`me@mydomain.com, team@mydomain.com` },
  ],
};

// Etiquetas de los campos: algunas son literales (URL, Chat ID) y otras se traducen.
function fieldText(i18n: ReturnType<typeof useLingui>['i18n'], v: MessageDescriptor | string) {
  return typeof v === 'string' ? v : i18n._(v);
}

function ChannelIcon({ type }: { type: ChannelType }) {
  if (type === 'email') return <Mail className="size-4" />;
  if (type === 'webhook') return <Webhook className="size-4" />;
  if (type === 'telegram') return <Send className="size-4" />;
  return <Bell className="size-4" />;
}

export function Notifications() {
  const { i18n } = useLingui();
  const { t } = useLinguiMacro();
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
      const name = c.name;
      toast.success(t`Test notification sent to ${name}`);
    } catch (e) {
      const name = c.name;
      toast.error(t`${name} isn't working`, { description: errorMessage(e), duration: 10_000 });
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
        <CardTitle className="text-lg">
          <Trans>Notifications</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>
            Alerts when a build fails, an SSL certificate has problems or a container goes down. Each alert is sent only
            once, not repeatedly.
          </Trans>
        </CardDescription>
        <CardAction>
          <Button size="sm" onClick={() => setEditing('new')}>
            <Plus /> <Trans>Add channel</Trans>
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        {channels && channels.length === 0 && (
          <p className="py-4 text-center text-sm text-muted-foreground">
            <Trans>No notification channels yet.</Trans>
          </p>
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
                        {i18n._(eventLabels[e])}
                      </Badge>
                    ))}
                  </div>
                  {c.last_error ? (
                    <p className="mt-1 flex items-center gap-1 text-xs text-destructive">
                      <TriangleAlert className="size-3" /> <Trans>Last delivery failed: {c.last_error}</Trans>
                    </p>
                  ) : (
                    c.last_sent_at && (
                      <p className="mt-1 text-xs text-muted-foreground">
                        <Trans>Last alert {timeAgo(c.last_sent_at)}</Trans>
                      </p>
                    )
                  )}
                </div>
                <Button variant="outline" size="sm" onClick={() => test(c)} disabled={testing === c.id}>
                  {testing === c.id ? <Loader2 className="animate-spin" /> : <Send />} <Trans>Test</Trans>
                </Button>
                <Switch checked={c.enabled} onCheckedChange={() => toggle(c)} aria-label={t`Active`} />
                <Button variant="ghost" size="icon-sm" onClick={() => setEditing(c)} aria-label={t`Edit`}>
                  <Pencil />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-destructive hover:text-destructive"
                  onClick={() => remove(c)}
                  aria-label={t`Delete`}
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
  const { i18n } = useLingui();
  const { t } = useLinguiMacro();
  const [type, setType] = useState<ChannelType>(channel?.type ?? 'discord');
  const [name, setName] = useState(channel?.name ?? '');
  const [events, setEvents] = useState<NotificationEvent[]>(
    channel?.events ?? ['build_failed', 'cert_error', 'cert_expiring', 'container_down', 'container_recovered', 'backup_failed', 'server_down', 'server_up'],
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
  const channelName = channel?.name;

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
      toast.success(channel ? t`Channel saved` : t`Channel created: click "Test" to check it`);
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
            <DialogTitle>{channel ? t`Edit ${channelName}` : t`New notification channel`}</DialogTitle>
            <DialogDescription>
              <Trans>Data is stored encrypted; secret fields are never shown again.</Trans>
            </DialogDescription>
          </DialogHeader>

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label>
                <Trans>Type</Trans>
              </Label>
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
              <Label htmlFor="ch-name">
                <Trans>Name</Trans>
              </Label>
              <Input id="ch-name" value={name} onChange={(e) => setName(e.target.value)} placeholder={t`Team`} required />
            </div>
          </div>

          {type === 'email' && (
            <div className="space-y-2">
              <Label>
                <Trans>Security</Trans>
              </Label>
              <Select value={config.security ?? 'starttls'} onValueChange={(v) => setConfig((c) => ({ ...c, security: v }))}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="starttls">
                    <Trans>STARTTLS (port 587)</Trans>
                  </SelectItem>
                  <SelectItem value="tls">
                    <Trans>TLS (port 465)</Trans>
                  </SelectItem>
                  <SelectItem value="none">
                    <Trans>Unencrypted (local servers only)</Trans>
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
          )}

          <div className="grid gap-4 sm:grid-cols-2">
            {fields[type].map((f) => (
              <div key={f.key} className={f.hint || f.key.endsWith('url') || f.key === 'to' ? 'space-y-2 sm:col-span-2' : 'space-y-2'}>
                <Label htmlFor={`ch-${f.key}`}>
                  {fieldText(i18n, f.label)}
                  {f.optional && (
                    <span className="font-normal text-muted-foreground">
                      {' '}
                      <Trans>(optional)</Trans>
                    </span>
                  )}
                </Label>
                <Input
                  id={`ch-${f.key}`}
                  type={f.secret ? 'password' : (f.type ?? 'text')}
                  value={config[f.key] ?? ''}
                  onChange={(e) => setConfig((c) => ({ ...c, [f.key]: e.target.value }))}
                  onFocus={(e) => f.secret && e.target.value === SECRET_MASK && setConfig((c) => ({ ...c, [f.key]: '' }))}
                  placeholder={f.placeholder && fieldText(i18n, f.placeholder)}
                  required={!f.optional}
                  autoComplete="off"
                />
                {f.hint && <p className="text-xs text-muted-foreground">{i18n._(f.hint)}</p>}
              </div>
            ))}
          </div>

          <div className="space-y-2">
            <Label>
              <Trans>Notify about</Trans>
            </Label>
            <div className="grid gap-2 sm:grid-cols-2">
              {(Object.keys(eventLabels) as NotificationEvent[]).map((ev) => (
                <CheckField
                  key={ev}
                  id={`ev-${ev}`}
                  checked={events.includes(ev)}
                  onChange={(v) => setEvents((list) => (v ? [...list, ev] : list.filter((x) => x !== ev)))}
                  label={i18n._(eventLabels[ev])}
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
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={busy || events.length === 0}>
              {busy && <Loader2 className="animate-spin" />}
              <Trans>Save</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
