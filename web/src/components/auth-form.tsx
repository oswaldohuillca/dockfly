import { useState } from 'react';
import { Loader2 } from 'lucide-react';
import { toast } from 'sonner';

import { Logo } from '@/components/logo';
import { RestoreBackup } from '@/components/restore-backup';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api, errorMessage, type User } from '@/lib/api';

interface Props {
  /** true en la primera instalación: crea el administrador en vez de iniciar sesión. */
  setup: boolean;
  onAuthenticated: (user: User) => void;
}

export function AuthForm({ setup, onAuthenticated }: Props) {
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
        onAuthenticated(await api.setup({ name, email, password }));
        return;
      }
      if (ticket) {
        const res = await api.login2FA({ ticket, code });
        if (res.used_recovery_code) {
          toast.warning(`Has usado un código de recuperación: te quedan ${res.recovery_codes_left}`, {
            description: 'Genera códigos nuevos en Mi cuenta si te quedan pocos.',
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
      if (ticket && errorMessage(err).includes('caducó')) {
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
              <CardTitle>Verificación en dos pasos</CardTitle>
              <CardDescription>
                {useRecovery
                  ? 'Escribe uno de tus códigos de recuperación. Cada código solo sirve una vez.'
                  : 'Escribe el código de 6 dígitos de tu app de autenticación.'}
              </CardDescription>
            </CardHeader>
            <CardContent>
              <form onSubmit={submit} className="space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="code">{useRecovery ? 'Código de recuperación' : 'Código'}</Label>
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
                  Verificar
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
                  {useRecovery ? 'Usar la app' : 'Usar un código de recuperación'}
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
                  Volver
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
            <CardTitle>{setup ? 'Bienvenido a Dockfly' : 'Iniciar sesión'}</CardTitle>
            <CardDescription>
              {setup ? 'Primera instalación: crea la cuenta de administrador.' : 'Accede al panel de administración.'}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <form onSubmit={submit} className="space-y-4">
              {setup && (
                <div className="space-y-2">
                  <Label htmlFor="name">Nombre</Label>
                  <Input id="name" value={name} onChange={(e) => setName(e.target.value)} required autoComplete="name" />
                </div>
              )}
              <div className="space-y-2">
                <Label htmlFor="email">Email</Label>
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
                <Label htmlFor="password">Contraseña</Label>
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
                {setup ? 'Crear administrador' : 'Entrar'}
              </Button>
            </form>
            {setup && (
              <div className="mt-4 flex flex-col items-center gap-1 border-t pt-4 text-center">
                <p className="text-xs text-muted-foreground">¿Vienes de otro servidor?</p>
                <RestoreBackup variant="link" />
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
