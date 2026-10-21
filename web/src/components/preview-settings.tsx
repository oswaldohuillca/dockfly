import { useEffect, useState } from 'react';
import { Lock, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';
import { Trans, useLingui } from '@lingui/react/macro';

import { AccessSelect } from '@/components/access-select';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api, errorMessage, type Settings } from '@/lib/api';

/** Dominio bajo el que se publican las previews de las ramas. */
export function PreviewSettings() {
  const { t } = useLingui();
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
      toast.success(t`Previews saved`);
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (!settings) return null;
  const dirty = domain !== settings.preview_domain || accessList !== settings.preview_access_list_id;
  const example = domain || t`previews.mydomain.com`;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">
          <Trans>Branch previews</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>
            Sites with previews enabled publish every push to another branch at{' '}
            <code className="font-mono">branch--site.{example}</code>, and remove it when the branch is deleted. Point{' '}
            <code className="font-mono">*.{example}</code> to this server in your DNS.
          </Trans>
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={save} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="preview-domain">
              <Trans>Preview domain</Trans>
            </Label>
            <Input
              id="preview-domain"
              value={domain}
              onChange={(e) => setDomain(e.target.value.trim())}
              placeholder={t`previews.mydomain.com`}
              className="font-mono"
            />
          </div>
          {settings.preview_domain && settings.preview_domain === domain && (
            <Alert>
              {settings.preview_ssl ? <Lock /> : <TriangleAlert />}
              <AlertDescription>
                {settings.preview_ssl
                  ? t`Previews use HTTPS with a wildcard certificate for *.${domain}.`
                  : t`Without a DNS zone or custom certificate for *.${domain}, previews only respond over HTTP. Add the zone under Certificates to use HTTPS.`}
              </AlertDescription>
            </Alert>
          )}
          <AccessSelect value={accessList} onChange={setAccessList} />
          <Button type="submit" size="sm" disabled={busy || !dirty}>
            <Trans>Save</Trans>
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}
