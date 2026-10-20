import { useRef, useState } from 'react';
import { Loader2, Upload } from 'lucide-react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Progress } from '@/components/ui/progress';
import { errorMessage, restoreBackup } from '@/lib/api';

/**
 * Restaura una copia: la sube, Dockfly se reinicia para aplicarla y, cuando
 * vuelve a responder, se recarga el panel (la sesión de la copia es otra).
 */
export function RestoreBackup({ variant = 'outline' }: { variant?: 'outline' | 'link' }) {
  const input = useRef<HTMLInputElement>(null);
  const [progress, setProgress] = useState<number | null>(null);
  const [restarting, setRestarting] = useState(false);

  async function restore(file: File) {
    if (!confirm(`¿Restaurar ${file.name}? Se reemplazarán todos los datos actuales (se guardan aparte por si acaso).`)) {
      return;
    }
    setProgress(0);
    try {
      const res = await restoreBackup(file, setProgress);
      setRestarting(true);
      toast.info(res.message);
      await waitForRestart();
      window.location.href = '/login';
    } catch (e) {
      toast.error('No se pudo restaurar', { description: errorMessage(e) });
      setProgress(null);
    }
  }

  if (restarting) {
    return (
      <p className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" /> Reiniciando Dockfly con la copia restaurada…
      </p>
    );
  }
  if (progress !== null) {
    return (
      <div className="space-y-1">
        <Progress value={progress * 100} className="max-w-xs" />
        <p className="text-xs text-muted-foreground">Subiendo la copia… {Math.round(progress * 100)}%</p>
      </div>
    );
  }
  return (
    <>
      <Button type="button" variant={variant} size="sm" onClick={() => input.current?.click()}>
        <Upload /> Restaurar una copia
      </Button>
      <input
        ref={input}
        type="file"
        accept=".tar.gz,.tgz,application/gzip"
        className="hidden"
        onChange={(e) => {
          const f = e.target.files?.[0];
          e.target.value = '';
          if (f) restore(f);
        }}
      />
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
  throw new Error('Dockfly no volvió a arrancar. Si no tiene política de reinicio, arráncalo a mano (docker start dockfly).');
}
