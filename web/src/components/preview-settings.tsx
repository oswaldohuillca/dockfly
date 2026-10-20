import { useEffect, useState } from 'react';
import { Lock, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';

import { AccessSelect } from '@/components/access-select';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api, errorMessage, type Settings } from '@/lib/api';

/** Dominio bajo el que se publican las previews de las ramas. */
export function PreviewSettings() {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [domain, setDomain] = useState('');
  const [accessList, setAccessList] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api
      .getSettings()
      .then((s) => {
        setSettings(s);
        setDomain(s.preview_domain);
        setAccessList(s.preview_access_list_id);
      })
      .catch((e) => toast.error(errorMessage(e)));
  }, []);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const res = await api.putSettings({ preview_domain: domain, preview_access_list_id: accessList ?? 0 });
      setSettings(res.settings);
      setDomain(res.settings.preview_domain);
      toast.success('Previews guardadas');
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (!settings) return null;
  const dirty = domain !== settings.preview_domain || accessList !== settings.preview_access_list_id;
  const example = domain || 'previews.midominio.com';

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">Previews por rama</CardTitle>
        <CardDescription>
          Los sitios con previews activadas publican cada push a otra rama en{' '}
          <code className="font-mono">rama--sitio.{example}</code>, y la borran cuando se borra la rama. Apunta{' '}
          <code className="font-mono">*.{example}</code> a este servidor en tu DNS.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={save} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="preview-domain">Dominio de previews</Label>
            <Input
              id="preview-domain"
              value={domain}
              onChange={(e) => setDomain(e.target.value.trim())}
              placeholder="previews.midominio.com"
              className="font-mono"
            />
          </div>
          {settings.preview_domain && settings.preview_domain === domain && (
            <Alert>
              {settings.preview_ssl ? <Lock /> : <TriangleAlert />}
              <AlertDescription>
                {settings.preview_ssl
                  ? `Las previews usan HTTPS con un certificado comodín para *.${domain}.`
                  : `Sin zona DNS ni certificado propio para *.${domain}, las previews solo responden por HTTP. Añade la zona en Certificados para usar HTTPS.`}
              </AlertDescription>
            </Alert>
          )}
          <AccessSelect value={accessList} onChange={setAccessList} />
          <Button type="submit" size="sm" disabled={busy || !dirty}>
            Guardar
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}
