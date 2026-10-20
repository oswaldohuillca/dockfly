import { useCallback, useEffect, useState } from 'react';
import { Copy, Download, Loader2, ShieldCheck, ShieldOff } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api, errorMessage, type TwoFactorSetup } from '@/lib/api';

type Flow = 'enable' | 'disable' | 'regenerate' | null;

/** Verificación en dos pasos de la cuenta (TOTP + códigos de recuperación). */
export function TwoFactor() {
  const [status, setStatus] = useState<{ enabled: boolean; recovery_codes_left: number } | null>(null);
  const [flow, setFlow] = useState<Flow>(null);

  const load = useCallback(() => {
    api
      .get2FA()
      .then(setStatus)
      .catch((e) => toast.error(errorMessage(e)));
  }, []);
  useEffect(load, [load]);

  if (!status) return null;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-lg">
          Verificación en dos pasos
          {status.enabled ? (
            <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
              Activada
            </Badge>
          ) : (
            <Badge variant="outline">Desactivada</Badge>
          )}
        </CardTitle>
        <CardDescription>
          Además de la contraseña, el panel pedirá un código de tu app de autenticación (Google Authenticator, 1Password,
          Bitwarden, Authy…). Quien robe tu contraseña no podrá entrar sin tu móvil.
        </CardDescription>
        <CardAction>
          {status.enabled ? (
            <Button variant="outline" size="sm" onClick={() => setFlow('disable')}>
              <ShieldOff /> Desactivar
            </Button>
          ) : (
            <Button size="sm" onClick={() => setFlow('enable')}>
              <ShieldCheck /> Activar
            </Button>
          )}
        </CardAction>
      </CardHeader>
      {status.enabled && (
        <CardContent className="flex flex-wrap items-center justify-between gap-2 text-sm">
          <span className={status.recovery_codes_left <= 2 ? 'text-destructive' : 'text-muted-foreground'}>
            Te quedan {status.recovery_codes_left} códigos de recuperación.
          </span>
          <Button variant="ghost" size="sm" onClick={() => setFlow('regenerate')}>
            Generar códigos nuevos
          </Button>
        </CardContent>
      )}
      {flow && (
        <TwoFactorDialog
          flow={flow}
          onClose={() => {
            setFlow(null);
            load();
          }}
        />
      )}
    </Card>
  );
}

function TwoFactorDialog({ flow, onClose }: { flow: Exclude<Flow, null>; onClose: () => void }) {
  const [password, setPassword] = useState('');
  const [setup, setSetup] = useState<TwoFactorSetup | null>(null);
  const [code, setCode] = useState('');
  const [codes, setCodes] = useState<string[] | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      if (flow === 'disable') {
        await api.disable2FA(password);
        toast.success('Verificación en dos pasos desactivada');
        onClose();
      } else if (flow === 'regenerate') {
        setCodes((await api.regenerateRecoveryCodes(password)).recovery_codes);
      } else if (!setup) {
        setSetup(await api.setup2FA(password));
      } else {
        setCodes((await api.enable2FA(code)).recovery_codes);
        toast.success('Verificación en dos pasos activada');
      }
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const title = {
    enable: 'Activar la verificación en dos pasos',
    disable: 'Desactivar la verificación en dos pasos',
    regenerate: 'Códigos de recuperación nuevos',
  }[flow];

  if (codes) {
    const text = codes.join('\n');
    return (
      <Dialog open onOpenChange={(open) => !open && onClose()}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Guarda tus códigos de recuperación</DialogTitle>
            <DialogDescription>
              Si pierdes el móvil, cada código te deja entrar una vez. Guárdalos en un lugar seguro (un gestor de
              contraseñas): no se volverán a mostrar.{flow === 'regenerate' && ' Los códigos anteriores ya no sirven.'}
            </DialogDescription>
          </DialogHeader>
          <ul className="grid grid-cols-2 gap-2 rounded-lg border bg-muted/40 p-4 font-mono text-sm">
            {codes.map((c) => (
              <li key={c}>{c}</li>
            ))}
          </ul>
          <DialogFooter className="gap-2 sm:justify-between">
            <div className="flex gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={() => navigator.clipboard.writeText(text).then(() => toast.success('Códigos copiados'))}
              >
                <Copy /> Copiar
              </Button>
              <Button variant="outline" size="sm" asChild>
                <a href={`data:text/plain;charset=utf-8,${encodeURIComponent(text + '\n')}`} download="dockfly-recovery-codes.txt">
                  <Download /> Descargar
                </a>
              </Button>
            </div>
            <Button size="sm" onClick={onClose}>
              Ya los he guardado
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    );
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="contents">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription>
              {setup
                ? 'Escanea el código con tu app de autenticación y escribe el código de 6 dígitos que aparece.'
                : 'Por seguridad, confirma tu contraseña.'}
            </DialogDescription>
          </DialogHeader>

          {setup ? (
            <div className="space-y-4">
              <div
                className="mx-auto size-48 overflow-hidden rounded-lg border bg-white p-1 [&>svg]:size-full"
                // SVG generado por el propio servidor a partir del secreto.
                dangerouslySetInnerHTML={{ __html: setup.qr_svg }}
              />
              <details className="text-xs text-muted-foreground">
                <summary className="cursor-pointer">¿No puedes escanearlo? Escribe la clave a mano</summary>
                <code className="mt-2 block break-all rounded bg-muted p-2 font-mono text-foreground">
                  {setup.secret.match(/.{1,4}/g)?.join(' ')}
                </code>
              </details>
              <div className="space-y-2">
                <Label htmlFor="totp-code">Código</Label>
                <Input
                  id="totp-code"
                  value={code}
                  onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                  required
                  autoFocus
                  autoComplete="one-time-code"
                  inputMode="numeric"
                  placeholder="123456"
                  className="font-mono tracking-widest"
                />
              </div>
            </div>
          ) : (
            <div className="space-y-2">
              <Label htmlFor="confirm-password">Contraseña</Label>
              <Input
                id="confirm-password"
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
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancelar
            </Button>
            <Button type="submit" variant={flow === 'disable' ? 'destructive' : 'default'} disabled={busy}>
              {busy && <Loader2 className="animate-spin" />}
              {flow === 'disable' ? 'Desactivar' : setup ? 'Activar' : 'Continuar'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
