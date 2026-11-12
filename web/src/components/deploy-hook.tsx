import { Copy, RefreshCw, Trash2, Webhook } from 'lucide-react';
import { toast } from 'sonner';
import { Trans, useLingui } from '@lingui/react/macro';

import { useConfirm } from '@/components/confirm';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api } from '@/lib/api';
import { useAction, useResource } from '@/lib/use-resource';

/**
 * Deploy hook de un sitio: la URL secreta que lanza un build al recibir un
 * POST (un CMS al publicar contenido, un cron…). Solo para quien puede desplegar.
 */
export function DeployHook({ siteId }: { siteId: number }) {
  const { t } = useLingui();
  const { data, setData, error } = useResource(() => api.getDeployHook(siteId));
  const run = useAction();
  const { confirm, dialog } = useConfirm();

  // Si falla la carga no se ofrece «crear»: regeneraría un hook que quizá ya existe.
  if (!data) return error ? <p className="px-6 text-sm text-destructive">{error}</p> : null;

  const url = data.token ? `${window.location.origin}/api/hooks/deploy/${data.token}` : '';
  const create = () => run(async () => setData(await api.createDeployHook(siteId)));

  async function regenerate() {
    const ok = await confirm({
      title: <Trans>Regenerate the deploy hook?</Trans>,
      description: <Trans>The current URL stops working: update it wherever you use it.</Trans>,
      action: <Trans>Regenerate</Trans>,
    });
    if (ok) await run(async () => setData(await api.createDeployHook(siteId)), { success: t`New deploy hook URL created` });
  }

  async function remove() {
    const ok = await confirm({
      title: <Trans>Delete the deploy hook?</Trans>,
      description: <Trans>Whatever calls this URL will no longer rebuild the site.</Trans>,
    });
    if (ok) await run(async () => setData(await api.deleteDeployHook(siteId)), { success: t`Deploy hook deleted` });
  }

  return (
    <div className="space-y-2 border-t px-6 pt-5">
      {dialog}
      <Label htmlFor="deploy-hook" className="flex items-center gap-1.5">
        <Webhook className="size-4 text-muted-foreground" /> <Trans>Deploy hook</Trans>
      </Label>
      {url ? (
        <>
          <div className="flex flex-wrap gap-2">
            <Input
              id="deploy-hook"
              readOnly
              value={url}
              onFocus={(e) => e.target.select()}
              className="min-w-0 flex-1 basis-64 font-mono text-xs"
            />
            <Button
              variant="outline"
              onClick={() => navigator.clipboard.writeText(url).then(() => toast.success(t`URL copied`))}
            >
              <Copy /> <Trans>Copy</Trans>
            </Button>
            <Button variant="outline" size="icon" onClick={regenerate} aria-label={t`Regenerate the deploy hook`}>
              <RefreshCw />
            </Button>
            <Button
              variant="outline"
              size="icon"
              className="text-destructive hover:text-destructive"
              onClick={remove}
              aria-label={t`Delete the deploy hook`}
            >
              <Trash2 />
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">
            <Trans>
              A <code className="font-mono">POST</code> to this URL rebuilds the site: paste it as a webhook in your
              CMS (Strapi, Contentful…) or call it from a cron. Anyone with the URL can trigger a build, so keep it
              private.
            </Trans>
          </p>
        </>
      ) : (
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className="max-w-xl text-sm text-muted-foreground">
            <Trans>
              Rebuild the site when something outside the repository changes, like content in a CMS: create a URL
              that triggers a build.
            </Trans>
          </p>
          <Button variant="outline" onClick={create}>
            <Trans>Create deploy hook</Trans>
          </Button>
        </div>
      )}
    </div>
  );
}
