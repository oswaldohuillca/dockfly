import { useRef, useState } from 'react';
import { Loader2, Upload } from 'lucide-react';
import { toast } from 'sonner';
import { t as coreT } from '@lingui/core/macro';
import { Trans, useLingui } from '@lingui/react/macro';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Progress } from '@/components/ui/progress';
import { errorMessage, restoreBackup } from '@/lib/api';

/**
 * Restaura una copia: la sube, Dockfly se reinicia para aplicarla y, cuando
 * vuelve a responder, se recarga el panel (la sesión de la copia es otra).
 */
export function RestoreBackup({
  variant = 'outline',
  setupToken,
}: {
  variant?: 'outline' | 'link';
  /** En una instalación nueva: el código de instalación (sin él, se pide la contraseña). */
  setupToken?: string;
}) {
  const { t } = useLingui();
  const input = useRef<HTMLInputElement>(null);
  const [progress, setProgress] = useState<number | null>(null);
  const [restarting, setRestarting] = useState(false);
  // La copia elegida, pendiente de confirmar.
  const [pending, setPending] = useState<File | null>(null);
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const fresh = setupToken !== undefined;

  async function restore(file: File) {
    setError('');
    setProgress(0);
    try {
      const res = await restoreBackup(file, fresh ? { setup_token: setupToken } : { password }, setProgress);
      setPending(null);
      setRestarting(true);
      toast.info(res.message);
      await waitForRestart();
      window.location.href = '/login';
    } catch (e) {
      // La contraseña o el código incorrectos se corrigen en el propio diálogo.
      if (pending) setError(errorMessage(e));
      else toast.error(t`Couldn't restore`, { description: errorMessage(e) });
      setProgress(null);
    }
  }

  if (restarting) {
    return (
      <p className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" /> <Trans>Restarting Dockfly with the restored backup…</Trans>
      </p>
    );
  }
  if (progress !== null && !pending) {
    const percent = Math.round(progress * 100);
    return (
      <div className="space-y-1">
        <Progress value={progress * 100} className="max-w-xs" />
        <p className="text-xs text-muted-foreground">
          <Trans>Uploading backup… {percent}%</Trans>
        </p>
      </div>
    );
  }
  return (
    <>
      <Button type="button" variant={variant} size="sm" onClick={() => input.current?.click()}>
        <Upload /> <Trans>Restore a backup</Trans>
      </Button>
      <input
        ref={input}
        type="file"
        accept=".tar.gz,.tgz,application/gzip"
        className="hidden"
        onChange={(e) => {
          const f = e.target.files?.[0];
          e.target.value = '';
          if (f) {
            setPassword('');
            setError('');
            setPending(f);
          }
        }}
      />
      {pending && (
        <Dialog open onOpenChange={(open) => !open && progress === null && setPending(null)}>
          <DialogContent className="sm:max-w-md">
            <form
              className="contents"
              onSubmit={(e) => {
                e.preventDefault();
                restore(pending);
              }}
            >
              <DialogHeader>
                <DialogTitle>
                  <Trans>Restore {pending.name}?</Trans>
                </DialogTitle>
                <DialogDescription>
                  <Trans>
                    All current data (users, sites, apps, keys) will be replaced by the backup's. The current data is
                    kept aside on the server, just in case.
                  </Trans>
                </DialogDescription>
              </DialogHeader>
              {!fresh && (
                <div className="space-y-2">
                  <Label htmlFor="restore-password">
                    <Trans>Confirm with your password</Trans>
                  </Label>
                  <Input
                    id="restore-password"
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    required
                    autoFocus
                    autoComplete="current-password"
                  />
                </div>
              )}
              {error && (
                <Alert variant="destructive">
                  <AlertDescription>{error}</AlertDescription>
                </Alert>
              )}
              {progress !== null && <Progress value={progress * 100} />}
              <DialogFooter>
                <Button type="button" variant="outline" onClick={() => setPending(null)} disabled={progress !== null}>
                  <Trans>Cancel</Trans>
                </Button>
                <Button type="submit" variant="destructive" disabled={progress !== null}>
                  {progress !== null && <Loader2 className="animate-spin" />}
                  <Trans>Restore</Trans>
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>
      )}
    </>
  );
}

/** Espera a que Dockfly se apague y vuelva a responder (máx. 2 minutos). */
async function waitForRestart() {
  const deadline = Date.now() + 120_000;
  let wentDown = false;
  while (Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 1000));
    try {
      const res = await fetch('/api/setup', { cache: 'no-store' });
      if (res.ok && wentDown) return;
      // Si no llegamos a verlo caído, damos un margen antes de seguir.
      if (res.ok && Date.now() > deadline - 110_000) return;
    } catch {
      wentDown = true;
    }
  }
  throw new Error(
    coreT`Dockfly didn't start again. If it has no restart policy, start it manually (docker start dockfly).`,
  );
}
