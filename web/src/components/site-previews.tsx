import { useCallback, useEffect, useState } from 'react';
import { ExternalLink, GitBranch, Trash2 } from 'lucide-react';
import { Trans, useLingui } from '@lingui/react/macro';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { api, errorMessage, type Preview, type Site } from '@/lib/api';
import { timeAgo } from '@/lib/format';

/** Ramas publicadas como preview. reload cambia cuando termina un build. */
export function SitePreviews({ site, reload }: { site: Site; reload: unknown }) {
  const { t } = useLingui();
  const [previews, setPreviews] = useState<Preview[]>([]);

  const load = useCallback(() => {
    api
      .listPreviews(site.id)
      .then(setPreviews)
      .catch(() => {});
  }, [site.id]);
  useEffect(load, [load, reload]);

  async function remove(p: Preview) {
    try {
      await api.deletePreview(site.id, p.preview);
      const branch = p.branch;
      toast.success(t`Preview for ${branch} deleted`);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  if (!site.previews && previews.length === 0) return null;

  const productionBranch = site.branch || t`production`;

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Previews</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>
            Every push to a branch other than <code className="font-mono">{productionBranch}</code> is published at
            its own URL. Previews are removed automatically when the branch is deleted.
          </Trans>
        </CardDescription>
      </CardHeader>
      <CardContent className="px-0">
        {previews.length === 0 ? (
          <p className="px-6 py-4 text-center text-sm text-muted-foreground">
            <Trans>No previews yet. Push to another branch to create the first one.</Trans>
          </p>
        ) : (
          <ul className="divide-y">
            {previews.map((p) => (
              <li key={p.preview} className="flex flex-wrap items-center gap-3 px-6 py-3">
                <GitBranch className="size-4 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="font-medium">{p.branch}</div>
                  <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                    <code className="font-mono">{p.deployment.commit_sha.slice(0, 7)}</code>
                    <span className="truncate">{p.deployment.commit_message}</span>
                    <span>· {timeAgo(p.deployment.created_at)}</span>
                  </div>
                </div>
                {p.url && (
                  <Button variant="outline" size="sm" asChild>
                    <a href={p.url} target="_blank" rel="noreferrer">
                      <ExternalLink /> <Trans>Open</Trans>
                    </a>
                  </Button>
                )}
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-destructive hover:text-destructive"
                  onClick={() => remove(p)}
                  aria-label={t`Delete preview for ${p.branch}`}
                >
                  <Trash2 />
                </Button>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
