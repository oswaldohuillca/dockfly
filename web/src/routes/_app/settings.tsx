import { createFileRoute, redirect } from '@tanstack/react-router';
import { Download, Terminal } from 'lucide-react';
import { Trans } from '@lingui/react/macro';

import { AccessLists } from '@/components/access-lists';
import { BackupStorageSettings } from '@/components/backup-storage';
import { CertificatesSettings } from '@/components/certificates-settings';
import { Notifications } from '@/components/notifications';
import { PreviewSettings } from '@/components/preview-settings';
import { RestoreBackup } from '@/components/restore-backup';
import { Users } from '@/components/users';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { can } from '@/lib/roles';

// Ruta donde se conservan los datos anteriores a una restauración.
const beforeRestoreDir = '/data/before-restore-<date>';

export const Route = createFileRoute('/_app/settings')({
  // Solo administradores (la API también lo exige).
  beforeLoad: ({ context }) => {
    if (!can(context.user, 'admin')) throw redirect({ to: '/' });
  },
  component: Settings,
});

function Settings() {
  return (
    <div className="space-y-4">
      <Users />
      <Notifications />
      <AccessLists />
      <CertificatesSettings />
      <PreviewSettings />
      <BackupStorageSettings />
      <Card>
        <CardHeader>
          <CardTitle className="text-lg">
            <Trans>Backups</Trans>
          </CardTitle>
          <CardDescription>
            <Trans>
              A backup includes the database, the keys (sessions and token encryption), the published sites and the
              Let&apos;s Encrypt certificates. Keep it somewhere safe: it can be used to read the tokens of your Git
              accounts.
            </Trans>
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex flex-wrap items-center gap-2">
            <Button size="sm" asChild>
              <a href="/api/backup" download>
                <Download /> <Trans>Download backup</Trans>
              </a>
            </Button>
            <RestoreBackup />
          </div>
          <p className="text-xs text-muted-foreground">
            <Trans>
              When restoring, Dockfly restarts and applies the backup; the previous data is kept in{' '}
              <code className="font-mono">{beforeRestoreDir}</code>.
            </Trans>
          </p>
          <details className="text-xs text-muted-foreground">
            <summary className="flex cursor-pointer items-center gap-1">
              <Terminal className="size-3.5" />{' '}
              <Trans>From the terminal (e.g. with a cron job, or if the dashboard won&apos;t start)</Trans>
            </summary>
            <pre className="mt-2 overflow-x-auto rounded bg-muted p-3 font-mono">
              {`docker exec dockfly dockfly backup /data/copia.tar.gz
docker cp dockfly:/data/copia.tar.gz .

# Restaurar: se aplica al volver a arrancar
docker cp copia.tar.gz dockfly:/data/ && docker exec dockfly dockfly restore /data/copia.tar.gz
docker restart dockfly`}
            </pre>
          </details>
        </CardContent>
      </Card>
    </div>
  );
}
