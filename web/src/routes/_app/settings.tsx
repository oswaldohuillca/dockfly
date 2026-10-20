import { createFileRoute } from '@tanstack/react-router';
import { Download, Terminal } from 'lucide-react';

import { AccessLists } from '@/components/access-lists';
import { CertificatesSettings } from '@/components/certificates-settings';
import { Notifications } from '@/components/notifications';
import { PreviewSettings } from '@/components/preview-settings';
import { RestoreBackup } from '@/components/restore-backup';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';

export const Route = createFileRoute('/_app/settings')({
  component: Settings,
});

function Settings() {
  return (
    <div className="space-y-4">
      <Notifications />
      <AccessLists />
      <CertificatesSettings />
      <PreviewSettings />
      <Card>
        <CardHeader>
          <CardTitle className="text-lg">Copias de seguridad</CardTitle>
          <CardDescription>
            Una copia incluye la base de datos, las claves (sesiones y cifrado de tokens), los sitios publicados y los
            certificados de Let&apos;s Encrypt. Guárdala en un lugar seguro: con ella se pueden leer los tokens de tus
            cuentas de Git.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex flex-wrap items-center gap-2">
            <Button size="sm" asChild>
              <a href="/api/backup" download>
                <Download /> Descargar copia
              </a>
            </Button>
            <RestoreBackup />
          </div>
          <p className="text-xs text-muted-foreground">
            Al restaurar, Dockfly se reinicia y aplica la copia; los datos que había se conservan en{' '}
            <code className="font-mono">/data/before-restore-&lt;fecha&gt;</code>.
          </p>
          <details className="text-xs text-muted-foreground">
            <summary className="flex cursor-pointer items-center gap-1">
              <Terminal className="size-3.5" /> Desde la terminal (p. ej. con un cron, o si el panel no arranca)
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
