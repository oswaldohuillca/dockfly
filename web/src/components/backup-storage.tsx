import { useEffect, useState } from 'react';
import { Loader2 } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { api, errorMessage, type BackupStorage } from '@/lib/api';

const empty: BackupStorage = { type: 'local', endpoint: '', region: '', bucket: '', prefix: 'dockfly', access_key: '', secret_key: '' };

/** Dónde se guardan las copias de las bases de datos. */
export function BackupStorageSettings() {
  const [form, setForm] = useState<BackupStorage | null>(null);
  const [location, setLocation] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api
      .getBackupStorage()
      .then((res) => {
        setForm({ ...empty, ...res.config, prefix: res.config.type === 's3' ? res.config.prefix : empty.prefix });
        setLocation(res.location);
      })
      .catch((e) => toast.error(errorMessage(e)));
  }, []);

  if (!form) return null;
  const set = <K extends keyof BackupStorage>(k: K, v: BackupStorage[K]) => setForm({ ...form, [k]: v });

  async function save(e: React.FormEvent) {
    e.preventDefault();
    if (!form) return;
    setError('');
    setBusy(true);
    try {
      const res = await api.putBackupStorage(form);
      setLocation(res.location);
      setForm({ ...form, secret_key: res.config.secret_key });
      toast.success('Almacenamiento guardado', { description: `Las copias se guardan en ${res.location}` });
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">Copias de las bases de datos</CardTitle>
        <CardDescription>
          Dónde se guardan las copias de las apps de PostgreSQL, MySQL y Redis (se programan en cada app). Con un bucket
          S3, las copias sobreviven aunque se pierda el servidor. Ahora: <code className="font-mono">{location}</code>
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={save} className="space-y-4">
          <ToggleGroup
            type="single"
            variant="outline"
            value={form.type}
            onValueChange={(v) => v && set('type', v as BackupStorage['type'])}
          >
            <ToggleGroupItem value="local">En este servidor</ToggleGroupItem>
            <ToggleGroupItem value="s3">Bucket S3</ToggleGroupItem>
          </ToggleGroup>
          {form.type === 's3' && (
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2 sm:col-span-2">
                <Label htmlFor="s3-endpoint">Endpoint</Label>
                <Input
                  id="s3-endpoint"
                  value={form.endpoint}
                  onChange={(e) => set('endpoint', e.target.value)}
                  placeholder="https://s3.eu-west-1.amazonaws.com"
                  required
                />
                <p className="text-xs text-muted-foreground">
                  AWS, Cloudflare R2 (<code className="font-mono">https://&lt;cuenta&gt;.r2.cloudflarestorage.com</code>
                  ), Backblaze B2, MinIO… cualquier almacenamiento compatible con S3.
                </p>
              </div>
              <div className="space-y-2">
                <Label htmlFor="s3-bucket">Bucket</Label>
                <Input id="s3-bucket" value={form.bucket} onChange={(e) => set('bucket', e.target.value)} required />
              </div>
              <div className="space-y-2">
                <Label htmlFor="s3-region">Región</Label>
                <Input
                  id="s3-region"
                  value={form.region}
                  onChange={(e) => set('region', e.target.value)}
                  placeholder="eu-west-1 (en R2: auto)"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="s3-key">Access key</Label>
                <Input id="s3-key" value={form.access_key} onChange={(e) => set('access_key', e.target.value)} required />
              </div>
              <div className="space-y-2">
                <Label htmlFor="s3-secret">Secret key</Label>
                <Input
                  id="s3-secret"
                  type="password"
                  value={form.secret_key}
                  onChange={(e) => set('secret_key', e.target.value)}
                  required
                  autoComplete="off"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="s3-prefix">Carpeta</Label>
                <Input id="s3-prefix" value={form.prefix} onChange={(e) => set('prefix', e.target.value)} placeholder="dockfly" />
              </div>
            </div>
          )}
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <Button type="submit" size="sm" disabled={busy}>
            {busy && <Loader2 className="animate-spin" />}
            {form.type === 's3' ? 'Probar y guardar' : 'Guardar'}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}
