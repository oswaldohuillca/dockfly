import { useState } from 'react';
import { Loader2 } from 'lucide-react';
import { Trans, useLingui } from '@lingui/react/macro';
import { toast } from 'sonner';

import { Logo } from '@/components/logo';
import { RestoreBackup } from '@/components/restore-backup';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api, ApiError, errorMessage, type User } from '@/lib/api';

interface Props {
  /** true en la primera instalación: crea el administrador en vez de iniciar sesión. */
  setup: boolean;
  /** La instalación exige el código de instalación (está en el log del servidor). */
  setupCodeRequired?: boolean;
  onAuthenticated: (user: User) => void;
}

export function AuthForm({ setup, setupCodeRequired, onAuthenticated }: Props) {
  const [setupCode, setSetupCode] = useState('');
  const { t } = useLingui();
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  // Con verificación en dos pasos, la contraseña da un ticket para pedir el código.
  const [ticket, setTicket] = useState('');
  const [code, setCode] = useState('');
  const [useRecovery, setUseRecovery] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      if (setup) {
        onAuthenticated(await api.setup({ name, email, password, setup_token: setupCode }));
        return;
      }
      if (ticket) {
        const res = await api.login2FA({ ticket, code });
        if (res.used_recovery_code) {
          const left = res.recovery_codes_left;
          toast.warning(t`You used a recovery code: ${left} left`, {
            description: t`Generate new codes in My account if you're running low.`,
            duration: 15000,
          });
        }
        onAuthenticated(res.user);
        return;
      }
      const res = await api.login({ email, password });
      if ('two_factor' in res) setTicket(res.ticket);
      else onAuthenticated(res);
    } catch (err) {
      setError(errorMessage(err));
      // El ticket caduca a los 5 minutos: hay que volver a la contraseña.
      if (ticket && err instanceof ApiError && err.code === 'login_expired') {
        setTicket('');
        setCode('');
      }
    } finally {
      setBusy(false);
    }
  }

  if (ticket) {
    return (
      <div className="grid min-h-svh place-items-center bg-muted/40 p-4">
        <div className="w-full max-w-sm space-y-6">
          <Logo className="justify-center text-2xl" />
          <Card>
            <CardHeader>
              <CardTitle>
                <Trans>Two-step verification</Trans>
              </CardTitle>
              <CardDescription>
                {useRecovery ? (
                  <Trans>Enter one of your recovery codes. Each code can only be used once.</Trans>
                ) : (
                  <Trans>Enter the 6-digit code from your authenticator app.</Trans>
                )}
              </CardDescription>
            </CardHeader>
            <CardContent>
              <form onSubmit={submit} className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="code">{useRecovery ? <Trans>Recovery code</Trans> : <Trans>Code</Trans>}</Label>
                  <Input
                    id="code"
                    value={code}
                    onChange={(e) => setCode(useRecovery ? e.target.value : e.target.value.replace(/\D/g, '').slice(0, 6))}
                    required
                    autoFocus
                    autoComplete="one-time-code"
                    inputMode={useRecovery ? 'text' : 'numeric'}
                    placeholder={useRecovery ? 'abcde-12345' : '123456'}
                    className="font-mono tracking-widest"
                  />
                </div>
                {error && (
                  <Alert variant="destructive">
                    <AlertDescription>{error}</AlertDescription>
                  </Alert>
                )}
                <Button type="submit" className="w-full" disabled={busy}>
                  {busy && <Loader2 className="animate-spin" />}
                  <Trans>Verify</Trans>
                </Button>
              </form>
              <div className="mt-4 flex justify-between border-t pt-3">
                <Button
                  variant="link"
                  size="sm"
                  className="px-0"
                  onClick={() => {
                    setUseRecovery(!useRecovery);
                    setCode('');
                    setError('');
                  }}
                >
                  {useRecovery ? <Trans>Use the app</Trans> : <Trans>Use a recovery code</Trans>}
                </Button>
                <Button
                  variant="link"
                  size="sm"
                  className="px-0 text-muted-foreground"
                  onClick={() => {
                    setTicket('');
                    setCode('');
                    setError('');
                  }}
                >
                  <Trans>Back</Trans>
                </Button>
              </div>
            </CardContent>
          </Card>
        </div>
      </div>
    );
  }

  return (
    <div className="grid min-h-svh place-items-center bg-muted/40 p-4">
      <div className="w-full max-w-sm space-y-6">
        <Logo className="justify-center text-2xl" />
        <Card>
          <CardHeader>
            <CardTitle>{setup ? <Trans>Welcome to Dockfly</Trans> : <Trans>Log in</Trans>}</CardTitle>
            <CardDescription>
              {setup ? (
                <Trans>First-time setup: create the administrator account.</Trans>
              ) : (
                <Trans>Sign in to the admin dashboard.</Trans>
              )}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <form onSubmit={submit} className="space-y-4">
              {setup && setupCodeRequired && (
                <div className="space-y-2">
                  <Label htmlFor="setup-code">
                    <Trans>Setup code</Trans>
                  </Label>
                  <Input
                    id="setup-code"
                    value={setupCode}
                    onChange={(e) => setSetupCode(e.target.value)}
                    required
                    autoComplete="off"
                    className="font-mono"
                    placeholder="xxxxxx-xxxxxx-xxxxxx-xxxxxx"
                  />
                  <p className="text-xs text-muted-foreground">
                    <Trans>
                      So nobody can claim your panel before you do. It's in the server logs (
                      <code className="font-mono">docker logs dockfly</code>) and in{' '}
                      <code className="font-mono">/data/setup-token</code>.
                    </Trans>
                  </p>
                </div>
              )}
              {setup && (
                <div className="space-y-2">
                  <Label htmlFor="name">
                    <Trans>Name</Trans>
                  </Label>
                  <Input id="name" value={name} onChange={(e) => setName(e.target.value)} required autoComplete="name" />
                </div>
              )}
              <div className="space-y-2">
                <Label htmlFor="email">
                  <Trans>Email</Trans>
                </Label>
                <Input
                  id="email"
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  required
                  autoComplete="email"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="password">
                  <Trans>Password</Trans>
                </Label>
                <Input
                  id="password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
                  minLength={setup ? 8 : undefined}
                  autoComplete={setup ? 'new-password' : 'current-password'}
                />
              </div>
              {error && (
                <Alert variant="destructive">
                  <AlertDescription>{error}</AlertDescription>
                </Alert>
              )}
              <Button type="submit" className="w-full" disabled={busy}>
                {busy && <Loader2 className="animate-spin" />}
                {setup ? <Trans>Create administrator</Trans> : <Trans>Sign in</Trans>}
              </Button>
            </form>
            {setup && (
              <div className="mt-4 flex flex-col items-center gap-1 border-t pt-4 text-center">
                <p className="text-xs text-muted-foreground">
                  <Trans>Coming from another server?</Trans>
                </p>
                <RestoreBackup variant="link" setupToken={setupCode} />
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
