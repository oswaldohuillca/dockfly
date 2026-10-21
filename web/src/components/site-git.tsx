import { useCallback, useEffect, useState } from 'react';
import { GitBranch, Play, Settings2, Unplug, Webhook } from 'lucide-react';
import { toast } from 'sonner';
import { Trans, useLingui } from '@lingui/react/macro';

import { ProviderIcon } from '@/components/brand-icons';
import { BuildLog } from '@/components/build-log';
import { BuildStatusBadge } from '@/components/build-status';
import { GitDialog } from '@/components/git-dialog';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { api, buildFinished, errorMessage, type Build, type GitConnection, type Site } from '@/lib/api';
import { timeAgo } from '@/lib/format';

interface Props {
  site: Site;
  /** Recarga el sitio (tras enlazar, o cuando un build publica). */
  onChange: () => void;
}

/** Tarjeta del repositorio enlazado y sus builds. */
export function SiteGit({ site, onChange }: Props) {
  const { t } = useLingui();
  const [configuring, setConfiguring] = useState(false);
  const [confirmUnlink, setConfirmUnlink] = useState(false);
  const [builds, setBuilds] = useState<Build[]>([]);
  const [viewing, setViewing] = useState<Build | null>(null);
  const [conn, setConn] = useState<GitConnection | null>(null);
  const linked = site.repo !== '';

  const loadBuilds = useCallback(async () => {
    if (!linked) return;
    try {
      setBuilds(await api.listBuilds(site.id));
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }, [linked, site.id]);

  useEffect(() => {
    loadBuilds();
  }, [loadBuilds]);

  useEffect(() => {
    if (site.git_connection_id === null) return;
    api.listConnections().then((list) => setConn(list.find((c) => c.id === site.git_connection_id) ?? null));
  }, [site.git_connection_id]);

  // Refrescamos la lista periódicamente: los builds que dispara un push
  // llegan por webhook, sin pasar por esta página. Más a menudo si hay uno en
  // marcha. Al terminar un build recargamos el sitio: hay un despliegue nuevo.
  const active = builds.some((b) => !buildFinished(b));
  useEffect(() => {
    if (!linked) return;
    const t = setInterval(async () => {
      const before = builds;
      const now = await api.listBuilds(site.id).catch(() => before);
      setBuilds(now);
      if (now.some((b) => buildFinished(b) && !before.find((x) => x.id === b.id && buildFinished(x)))) onChange();
    }, active ? 2500 : 5000);
    return () => clearInterval(t);
  }, [linked, active, builds, site.id, onChange]);

  async function deploy() {
    try {
      const b = await api.triggerBuild(site.id);
      await loadBuilds();
      setViewing(b);
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  async function unlink() {
    try {
      await api.deleteSiteGit(site.id);
      toast.success(t`Repository disconnected`);
      setBuilds([]);
      onChange();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  const onBuildChange = useCallback(() => {
    loadBuilds();
    onChange();
  }, [loadBuilds, onChange]);

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>
            <Trans>Repository</Trans>
          </CardTitle>
          {linked ? (
            <CardDescription className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span className="flex items-center gap-1.5 font-medium text-foreground">
                {conn && <ProviderIcon provider={conn.provider} />}
                {site.repo}
              </span>
              <span className="flex items-center gap-1">
                <GitBranch className="size-3.5" /> {site.branch}
              </span>
              {site.webhook_id ? (
                <Badge variant="secondary" className="gap-1">
                  <Webhook /> <Trans>Deploys on every push</Trans>
                </Badge>
              ) : (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Badge variant="outline" className="gap-1 text-muted-foreground">
                      <Webhook /> <Trans>Manual only</Trans>
                    </Badge>
                  </TooltipTrigger>
                  <TooltipContent className="max-w-xs">
                    <Trans>
                      Set Dockfly's public URL in Integrations and save the build settings again to deploy on every
                      push.
                    </Trans>
                  </TooltipContent>
                </Tooltip>
              )}
            </CardDescription>
          ) : (
            <CardDescription>
              <Trans>Connect a GitHub or GitLab repository to build and publish the site on every push.</Trans>
            </CardDescription>
          )}
          <CardAction className="flex gap-2">
            {linked ? (
              <>
                <Button size="sm" onClick={deploy}>
                  <Play /> <Trans>Deploy now</Trans>
                </Button>
                <Button size="sm" variant="outline" onClick={() => setConfiguring(true)} aria-label={t`Configure`}>
                  <Settings2 />
                </Button>
                <Button size="sm" variant="outline" onClick={() => setConfirmUnlink(true)} aria-label={t`Disconnect`}>
                  <Unplug />
                </Button>
              </>
            ) : (
              <Button size="sm" variant="outline" onClick={() => setConfiguring(true)}>
                <Trans>Connect repository</Trans>
              </Button>
            )}
          </CardAction>
        </CardHeader>

        {linked && (
          <CardContent className="px-0">
            {builds.length === 0 ? (
              <p className="px-6 py-4 text-center text-sm text-muted-foreground">
                <Trans>No builds yet. Push to {site.branch} or click Deploy now.</Trans>
              </p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="pl-6">
                      <Trans>Build</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Commit</Trans>
                    </TableHead>
                    <TableHead>
                      <Trans>Trigger</Trans>
                    </TableHead>
                    <TableHead className="pr-6">
                      <Trans>Date</Trans>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {builds.map((b) => (
                    <TableRow key={b.id} className="cursor-pointer" onClick={() => setViewing(b)}>
                      <TableCell className="pl-6">
                        <div className="flex items-center gap-2">
                          <span className="font-mono text-sm">#{b.id}</span>
                          <BuildStatusBadge status={b.status} />
                        </div>
                        {b.preview && <div className="mt-0.5 text-xs text-muted-foreground"><Trans>Preview · {b.branch}</Trans></div>}
                      </TableCell>
                      <TableCell className="max-w-72">
                        {b.commit_sha ? (
                          <div className="flex items-center gap-2 truncate">
                            <code className="font-mono text-xs text-muted-foreground">{b.commit_sha.slice(0, 7)}</code>
                            <span className="truncate">{b.commit_message}</span>
                          </div>
                        ) : (
                          <span className="text-muted-foreground">
                            <Trans>latest commit on {b.branch}</Trans>
                          </span>
                        )}
                        {b.status === 'failed' && b.error && (
                          <div className="truncate text-xs text-destructive">{b.error}</div>
                        )}
                      </TableCell>
                      <TableCell className="text-muted-foreground">{b.trigger === 'push' ? t`Push` : t`Manual`}</TableCell>
                      <TableCell className="pr-6 text-muted-foreground">{timeAgo(b.created_at)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        )}
      </Card>

      {configuring && (
        <GitDialog
          site={site}
          onClose={() => setConfiguring(false)}
          onSaved={(webhookError) => {
            setConfiguring(false);
            if (webhookError) toast.warning(t`No automatic deploys`, { description: webhookError });
            onChange();
          }}
        />
      )}
      {viewing && <BuildLog build={viewing} onClose={() => setViewing(null)} onChange={onBuildChange} />}

      <AlertDialog open={confirmUnlink} onOpenChange={setConfirmUnlink}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              <Trans>Disconnect {site.repo}?</Trans>
            </AlertDialogTitle>
            <AlertDialogDescription>
              <Trans>
                The webhook is deleted and pushes will no longer deploy. The site and its deployments are kept.
              </Trans>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>
              <Trans>Cancel</Trans>
            </AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={unlink}>
              <Trans>Disconnect</Trans>
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
