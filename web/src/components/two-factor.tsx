import { useCallback, useEffect, useState } from 'react';
import { Copy, Download, Loader2, ShieldCheck, ShieldOff } from 'lucide-react';
import { Plural, Trans, useLingui } from '@lingui/react/macro';
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
          <Trans>Two-step verification</Trans>
          {status.enabled ? (
            <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
              <Trans context="2fa">Enabled</Trans>
            </Badge>
          ) : (
            <Badge variant="outline">
              <Trans>Disabled</Trans>
            </Badge>
          )}
        </CardTitle>
        <CardDescription>
          <Trans>
            Besides your password, the dashboard will ask for a code from your authenticator app (Google Authenticator,
            1Password, Bitwarden, Authy…). Anyone who steals your password won't be able to sign in without your phone.
          </Trans>
        </CardDescription>
        <CardAction>
          {status.enabled ? (
            <Button variant="outline" size="sm" onClick={() => setFlow('disable')}>
              <ShieldOff /> <Trans>Disable</Trans>
            </Button>
          ) : (
            <Button size="sm" onClick={() => setFlow('enable')}>
              <ShieldCheck /> <Trans>Enable</Trans>
            </Button>
          )}
        </CardAction>
      </CardHeader>
      {status.enabled && (
        <CardContent className="flex flex-wrap items-center justify-between gap-2 text-sm">
          <span className={status.recovery_codes_left <= 2 ? 'text-destructive' : 'text-muted-foreground'}>
            <Plural
              value={status.recovery_codes_left}
              one="You have # recovery code left."
              other="You have # recovery codes left."
            />
          </span>
          <Button variant="ghost" size="sm" onClick={() => setFlow('regenerate')}>
            <Trans>Generate new codes</Trans>
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
  const { t } = useLingui();
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
        toast.success(t`Two-step verification disabled`);
        onClose();
      } else if (flow === 'regenerate') {
        setCodes((await api.regenerateRecoveryCodes(password)).recovery_codes);
      } else if (!setup) {
        setSetup(await api.setup2FA(password));
      } else {
        setCodes((await api.enable2FA(code)).recovery_codes);
        toast.success(t`Two-step verification enabled`);
      }
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const title = {
    enable: t`Enable two-step verification`,
    disable: t`Disable two-step verification`,
    regenerate: t`New recovery codes`,
  }[flow];

  if (codes) {
    const text = codes.join('\n');
    return (
      <Dialog open onOpenChange={(open) => !open && onClose()}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              <Trans>Save your recovery codes</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>
                If you lose your phone, each code lets you sign in once. Keep them somewhere safe (a password manager):
                they won't be shown again.
              </Trans>
              {flow === 'regenerate' && (
                <>
                  {' '}
                  <Trans>Your previous codes no longer work.</Trans>
                </>
              )}
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
                onClick={() => navigator.clipboard.writeText(text).then(() => toast.success(t`Codes copied`))}
              >
                <Copy /> <Trans>Copy</Trans>
              </Button>
              <Button variant="outline" size="sm" asChild>
                <a href={`data:text/plain;charset=utf-8,${encodeURIComponent(text + '\n')}`} download="dockfly-recovery-codes.txt">
                  <Download /> <Trans>Download</Trans>
                </a>
              </Button>
            </div>
            <Button size="sm" onClick={onClose}>
              <Trans>I've saved them</Trans>
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
              {setup ? (
                <Trans>Scan the code with your authenticator app and enter the 6-digit code it shows.</Trans>
              ) : (
                <Trans>For security, confirm your password.</Trans>
              )}
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
                <summary className="cursor-pointer">
                  <Trans>Can't scan it? Enter the key manually</Trans>
                </summary>
                <code className="mt-2 block break-all rounded bg-muted p-2 font-mono text-foreground">
                  {setup.secret.match(/.{1,4}/g)?.join(' ')}
                </code>
              </details>
              <div className="space-y-2">
                <Label htmlFor="totp-code">
                  <Trans>Code</Trans>
                </Label>
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
              <Label htmlFor="confirm-password">
                <Trans>Password</Trans>
              </Label>
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
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" variant={flow === 'disable' ? 'destructive' : 'default'} disabled={busy}>
              {busy && <Loader2 className="animate-spin" />}
              {flow === 'disable' ? <Trans>Disable</Trans> : setup ? <Trans>Enable</Trans> : <Trans>Continue</Trans>}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
