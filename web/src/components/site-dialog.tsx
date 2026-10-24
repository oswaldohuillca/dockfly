import { useEffect, useState } from 'react';
import { Trans, useLingui } from '@lingui/react/macro';
import { toast } from 'sonner';

import { AccessSelect } from '@/components/access-select';
import { CheckField } from '@/components/check-field';
import { DomainsInput } from '@/components/domains-input';
import { FormDialog } from '@/components/form-dialog';
import { ProxyLocations } from '@/components/proxy-locations';
import { SSLFields } from '@/components/ssl-fields';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { api, type Site, type SiteInput } from '@/lib/api';
import { useForm, useSubmit } from '@/lib/use-form';

interface Props {
  /** Sitio a editar, o undefined para crear uno nuevo. */
  site?: Site;
  onClose: () => void;
  onSaved: (site: Site) => void;
}

export function SiteDialog({ site, onClose, onSaved }: Props) {
  const { t } = useLingui();
  const isNew = site === undefined;
  const { form, set } = useForm<SiteInput>(() => ({
    name: site?.name ?? '',
    domains: [...(site?.domains ?? [])],
    ssl: site?.ssl ?? false,
    force_https: site?.force_https ?? true,
    hsts: site?.hsts ?? false,
    spa: site?.spa ?? false,
    previews: site?.previews ?? false,
    access_list_id: site?.access_list_id ?? null,
    locations: (site?.locations ?? []).map((l) => ({ ...l })),
  }));

  // Los contenedores, para sugerirlos, solo si alguna ubicación va a uno. Sin
  // Docker la lista queda vacía y el nombre se escribe a mano.
  const [containers, setContainers] = useState<string[] | null>(null);
  const wantsContainers = form.locations.some((l) => l.target_type === 'docker');
  useEffect(() => {
    if (!wantsContainers || containers !== null) return;
    api
      .listContainers()
      .then((res) => setContainers(res.containers.map((c) => c.name)))
      .catch(() => setContainers([]));
  }, [wantsContainers, containers]);

  const { submit, busy, error } = useSubmit(async () => {
    const saved = isNew ? await api.createSite(form) : await api.updateSite(site.id, form);
    toast.success(isNew ? t`Site created` : t`Changes saved`);
    onSaved(saved);
  });

  return (
    <FormDialog
      className="sm:max-w-xl"
      title={isNew ? <Trans>New site</Trans> : <Trans>Edit {site.name}</Trans>}
      description={
        isNew ? <Trans>You can upload your build files afterwards.</Trans> : <Trans>Changes apply instantly.</Trans>
      }
      onClose={onClose}
      onSubmit={submit}
      busy={busy}
      error={error}
      submitLabel={isNew ? <Trans>Create site</Trans> : <Trans>Save</Trans>}
    >
      <Tabs defaultValue="details">
        <TabsList className="w-full">
          <TabsTrigger value="details">
            <Trans>Details</Trans>
          </TabsTrigger>
          <TabsTrigger value="locations">
            <Trans>Custom locations</Trans>
            {form.locations.length > 0 && (
              <Badge variant="secondary" className="ml-1 px-1.5">
                {form.locations.length}
              </Badge>
            )}
          </TabsTrigger>
          <TabsTrigger value="ssl">
            <Trans>SSL and access</Trans>
          </TabsTrigger>
        </TabsList>

        <TabsContent value="details" className="space-y-5 pt-3">
          <div className="space-y-2">
            <Label htmlFor="site-name">
              <Trans>Name</Trans>
            </Label>
            <Input
              id="site-name"
              value={form.name}
              onChange={(e) => set('name', e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-'))}
              placeholder={t`my-site`}
              required
              autoFocus={isNew}
            />
            <p className="text-xs text-muted-foreground">
              <Trans>Lowercase letters, numbers and hyphens.</Trans>
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="site-domains">
              <Trans>Domains</Trans>
            </Label>
            <DomainsInput id="site-domains" value={form.domains} onChange={(d) => set('domains', d)} />
          </div>
          <CheckField
            id="spa"
            checked={form.spa}
            onChange={(v) => set('spa', v)}
            label={t`Single-page application (SPA)`}
            hint={t`Serves index.html for any path that doesn't exist. Turn it on for React Router, Vue Router, etc.`}
          />
          <CheckField
            id="previews"
            checked={form.previews}
            onChange={(v) => set('previews', v)}
            label={t`Branch previews`}
            hint={t`Every push to another branch is published at its own URL (branch--site.previews-domain). Set the domain in Settings.`}
          />
        </TabsContent>

        <TabsContent value="locations" className="pt-3">
          <ProxyLocations
            value={form.locations}
            onChange={(l) => set('locations', l)}
            containers={containers ?? []}
            owner="site"
          />
        </TabsContent>

        <TabsContent value="ssl" className="space-y-6 pt-3">
          <SSLFields value={form} onChange={set} />
          <AccessSelect value={form.access_list_id} onChange={(id) => set('access_list_id', id)} />
        </TabsContent>
      </Tabs>
    </FormDialog>
  );
}
