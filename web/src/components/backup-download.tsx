import { useState } from 'react';
import { Download, Loader2 } from 'lucide-react';
import { toast } from 'sonner';
import { Trans, useLingui } from '@lingui/react/macro';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { downloadBackup, errorMessage } from '@/lib/api';

/**
 * Descarga la copia completa. Con una contraseña (opcional), el servidor la
 * cifra antes de enviarla: la copia incluye las claves, así que cifrarla al
 * sacarla del servidor es recomendable.
 */
export function BackupDownload() {
  const { t } = useLingui();
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);

  async function run() {
    setBusy(true);
    try {
      const blob = await downloadBackup(password || undefined);
      const stamp = new Date().toISOString().slice(0, 16).replace(/[-:T]/g, '').replace(/(\d{8})(\d{4})/, '$1-$2');
      const name = `dockfly-backup-${stamp}.tar.gz${password ? '.enc' : ''}`;
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = name;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      toast.error(t`Couldn't download the backup`, { description: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-wrap items-end gap-2">
      <div className="space-y-1">
        <label htmlFor="backup-password" className="text-xs text-muted-foreground">
          <Trans>Password (optional, encrypts the backup)</Trans>
        </label>
        <Input
          id="backup-password"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete="new-password"
          className="max-w-xs"
        />
      </div>
      <Button size="sm" onClick={run} disabled={busy}>
        {busy ? <Loader2 className="animate-spin" /> : <Download />} <Trans>Download backup</Trans>
      </Button>
    </div>
  );
}
