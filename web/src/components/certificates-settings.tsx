import { useCallback, useEffect, useState } from 'react';
import { FileKey, Globe, Loader2, Plus, Trash2, TriangleAlert, Upload } from 'lucide-react';
import { toast } from 'sonner';
import { Trans, useLingui } from '@lingui/react/macro';

import { DomainsInput } from '@/components/domains-input';
import { EnvEditor, envToRows, rowsToEnv, type EnvRow } from '@/components/env-editor';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import {
  api,
  errorMessage,
  SECRET_MASK,
  type CustomCertificate,
  type DNSProvider,
  type DNSProviderInfo,
} from '@/lib/api';
import { formatDate } from '@/lib/format';

export function CertificatesSettings() {
  const { t } = useLingui();
  const [catalog, setCatalog] = useState<DNSProviderInfo[]>([]);
  const [providers, setProviders] = useState<DNSProvider[]>([]);
  const [certs, setCerts] = useState<CustomCertificate[]>([]);
  const [editing, setEditing] = useState<DNSProvider | 'new' | null>(null);
  const [uploading, setUploading] = useState(false);

  const load = useCallback(() => {
    api.listDNSProviders().then(setProviders).catch((e) => toast.error(errorMessage(e)));
    api.listCustomCerts().then(setCerts).catch(() => {});
  }, []);
  useEffect(() => {
    api.dnsCatalog().then(setCatalog);
    load();
  }, [load]);

  const name = (code: string) => catalog.find((c) => c.code === code)?.name ?? code;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">
          <Trans>Certificates</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>
            By default, Let&apos;s Encrypt validates each domain over HTTP. For wildcard domains (*.example.com) or servers
            without port 80 open, set up the zone with its DNS provider: hosts in that zone will use the DNS challenge
            automatically.
          </Trans>
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-6">
        <section className="space-y-2">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-medium">
              <Trans>DNS zones</Trans>
            </h3>
            <Button size="sm" variant="outline" onClick={() => setEditing('new')}>
              <Plus /> <Trans>Add provider</Trans>
            </Button>
          </div>
          {providers.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              <Trans>None: all certificates are validated over HTTP.</Trans>
            </p>
          ) : (
            <ul className="divide-y rounded-lg border">
              {providers.map((p) => (
                <li key={p.id} className="flex flex-wrap items-center gap-3 px-4 py-3">
                  <Globe className="size-4 text-muted-foreground" />
                  <div className="min-w-0 flex-1">
                    <div className="font-medium">{name(p.provider)}</div>
                    <div className="mt-1 flex flex-wrap gap-1">
                      {p.zones.map((z) => (
                        <Badge key={z} variant="secondary" className="font-mono font-normal">
                          {z} · *.{z}
                        </Badge>
                      ))}
                    </div>
                  </div>
                  <Button variant="ghost" size="sm" onClick={() => setEditing(p)}>
                    <Trans>Edit</Trans>
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="text-destructive hover:text-destructive"
                    onClick={() => api.deleteDNSProvider(p.id).then(load).catch((e) => toast.error(errorMessage(e)))}
                    aria-label={t`Delete`}
                  >
                    <Trash2 />
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </section>

        <section className="space-y-2">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-medium">
              <Trans>Custom certificates</Trans>
            </h3>
            <Button size="sm" variant="outline" onClick={() => setUploading(true)}>
              <Upload /> <Trans>Upload certificate</Trans>
            </Button>
          </div>
          {certs.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              <Trans>None. If you upload one, it will be used automatically for the domains it covers.</Trans>
            </p>
          ) : (
            <ul className="divide-y rounded-lg border">
              {certs.map((c) => {
                const issuer = c.issuer || t`unknown issuer`;
                const expires = formatDate(c.not_after);
                return (
                  <li key={c.id} className="flex flex-wrap items-center gap-3 px-4 py-3">
                    <FileKey className="size-4 text-muted-foreground" />
                    <div className="min-w-0 flex-1">
                      <div className="font-medium">{c.name}</div>
                      <div className="text-xs text-muted-foreground">
                        {c.domains.join(', ')} · {issuer} · <Trans>expires on {expires}</Trans>
                      </div>
                    </div>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      className="text-destructive hover:text-destructive"
                      onClick={() => api.deleteCustomCert(c.id).then(load).catch((e) => toast.error(errorMessage(e)))}
                      aria-label={t`Delete`}
                    >
                      <Trash2 />
                    </Button>
                  </li>
                );
              })}
            </ul>
          )}
        </section>
      </CardContent>

      {editing && (
        <DNSDialog
          catalog={catalog}
          provider={editing === 'new' ? undefined : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            load();
          }}
        />
      )}
      {uploading && (
        <UploadCertDialog
          onClose={() => setUploading(false)}
          onSaved={() => {
            setUploading(false);
            load();
          }}
        />
      )}
    </Card>
  );
}

function DNSDialog({ catalog, provider, onClose, onSaved }: {
  catalog: DNSProviderInfo[]; provider?: DNSProvider; onClose: () => void; onSaved: () => void;
}) {
  const { t } = useLingui();
  const known = (code: string) => catalog.some((c) => c.code === code);
  const [code, setCode] = useState(provider ? (known(provider.provider) ? provider.provider : 'custom') : 'cloudflare');
  const [customCode, setCustomCode] = useState(provider && !known(provider.provider) ? provider.provider : '');
  const [zones, setZones] = useState<string[]>(provider?.zones ?? []);
  const [env, setEnv] = useState<Record<string, string>>(provider?.env ?? {});
  const [rows, setRows] = useState<EnvRow[]>(provider && !known(provider.provider) ? envToRows(provider.env) : []);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const info = catalog.find((c) => c.code === code);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      await api.saveDNSProvider({
        provider: code === 'custom' ? customCode : code,
        zones,
        env: code === 'custom' ? rowsToEnv(rows) : env,
      });
      toast.success(t`DNS provider saved`, { description: t`Traefik restarted to load the credentials.` });
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
            <DialogTitle>{provider ? <Trans>Edit DNS provider</Trans> : <Trans>DNS provider</Trans>}</DialogTitle>
            <DialogDescription>
              <Trans>
                Let&apos;s Encrypt will verify that you control the domain by creating a temporary TXT record. Credentials
                are stored encrypted.
              </Trans>
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label>
              <Trans>Provider</Trans>
            </Label>
            <Select value={code} onValueChange={(v) => { setCode(v); setEnv({}); }} disabled={!!provider}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {catalog.map((c) => (
                  <SelectItem key={c.code} value={c.code}>
                    {c.name}
                  </SelectItem>
                ))}
                <SelectItem value="custom">
                  <Trans>Other (any lego provider)</Trans>
                </SelectItem>
              </SelectContent>
            </Select>
            {info?.help && <p className="text-xs text-muted-foreground">{info.help}</p>}
          </div>
          <div className="space-y-2">
            <Label htmlFor="dns-zones">
              <Trans>Zones</Trans>
            </Label>
            <DomainsInput id="dns-zones" value={zones} onChange={setZones} />
            <p className="text-xs text-muted-foreground">
              <Trans>Each zone covers the domain, its subdomains and its wildcard.</Trans>
            </p>
          </div>
          {code === 'custom' ? (
            <>
              <div className="space-y-2">
                <Label htmlFor="dns-code">
                  <Trans>Provider code</Trans>
                </Label>
                <Input id="dns-code" className="font-mono" value={customCode} onChange={(e) => setCustomCode(e.target.value)} placeholder="hetzner" required disabled={!!provider} />
                <p className="text-xs text-muted-foreground">
                  <Trans>
                    Each provider&apos;s code and variables are listed in{' '}
                    <a className="underline" href="https://doc.traefik.io/traefik/https/acme/#providers" target="_blank" rel="noreferrer">
                      the Traefik documentation
                    </a>
                    .
                  </Trans>
                </p>
              </div>
              <div className="space-y-2">
                <Label>
                  <Trans>Variables</Trans>
                </Label>
                <EnvEditor rows={rows} onChange={setRows} />
              </div>
            </>
          ) : (
            info?.fields.map((f) => (
              <div key={f.env} className="space-y-2">
                <Label htmlFor={f.env}>
                  {f.label}
                  {f.optional && <span className="font-normal text-muted-foreground">
                      {' '}
                      <Trans>(optional)</Trans>
                    </span>}
                </Label>
                <Input
                  id={f.env}
                  type={f.secret ? 'password' : 'text'}
                  className="font-mono"
                  value={env[f.env] ?? ''}
                  onChange={(e) => setEnv((v) => ({ ...v, [f.env]: e.target.value }))}
                  onFocus={(e) => f.secret && e.target.value === SECRET_MASK && setEnv((v) => ({ ...v, [f.env]: '' }))}
                  required={!f.optional}
                  autoComplete="off"
                />
              </div>
            ))
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
            <Button type="submit" disabled={busy || zones.length === 0}>
              {busy && <Loader2 className="animate-spin" />}
              <Trans>Save</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function UploadCertDialog({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const { t } = useLingui();
  const [name, setName] = useState('');
  const [cert, setCert] = useState('');
  const [key, setKey] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const readFile = (set: (v: string) => void) => (e: React.ChangeEvent<HTMLInputElement>) => {
    e.target.files?.[0]?.text().then(set);
  };

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      await api.uploadCustomCert({ name, cert, key });
      toast.success(t`Certificate uploaded`);
      onSaved();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const area = 'min-h-28 w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-xs outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50';
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-lg">
        <form onSubmit={save} className="contents">
          <DialogHeader>
            <DialogTitle>
              <Trans>Upload certificate</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>In PEM format. Include the intermediate chain after the certificate.</Trans>
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="cert-name">
              <Trans>Name</Trans>
            </Label>
            <Input id="cert-name" value={name} onChange={(e) => setName(e.target.value)} placeholder={t`(the first domain)`} />
          </div>
          <div className="space-y-2">
            <Label htmlFor="cert-pem">
              <Trans>Certificate</Trans>
            </Label>
            <textarea id="cert-pem" className={area} value={cert} onChange={(e) => setCert(e.target.value)} placeholder="-----BEGIN CERTIFICATE-----" required />
            <Input type="file" accept=".pem,.crt,.cer" onChange={readFile(setCert)} />
          </div>
          <div className="space-y-2">
            <Label htmlFor="key-pem">
              <Trans>Private key</Trans>
            </Label>
            <textarea id="key-pem" className={area} value={key} onChange={(e) => setKey(e.target.value)} placeholder="-----BEGIN PRIVATE KEY-----" required />
            <Input type="file" accept=".pem,.key" onChange={readFile(setKey)} />
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
            <Button type="submit" disabled={busy}>
              {busy && <Loader2 className="animate-spin" />}
              <Trans>Upload</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
