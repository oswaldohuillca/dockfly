import { useCallback, useEffect, useState } from 'react';
import { ExternalLink, GitBranch, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { api, errorMessage, type Preview, type Site } from '@/lib/api';
import { timeAgo } from '@/lib/format';

/** Ramas publicadas como preview. reload cambia cuando termina un build. */
export function SitePreviews({ site, reload }: { site: Site; reload: unknown }) {
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
      toast.success(`Preview de ${p.branch} borrada`);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  if (!site.previews && previews.length === 0) return null;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Previews</CardTitle>
        <CardDescription>
          Cada push a una rama distinta de <code className="font-mono">{site.branch || 'producción'}</code> se publica
          en su propia URL. Se borran solas al borrar la rama.
        </CardDescription>
      </CardHeader>
      <CardContent className="px-0">
        {previews.length === 0 ? (
          <p className="px-6 py-4 text-center text-sm text-muted-foreground">
            Aún no hay previews. Haz push a otra rama para crear la primera.
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
                      <ExternalLink /> Abrir
                    </a>
                  </Button>
                )}
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-destructive hover:text-destructive"
                  onClick={() => remove(p)}
                  aria-label={`Borrar la preview de ${p.branch}`}
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
