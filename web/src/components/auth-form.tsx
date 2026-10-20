import { useState } from 'react';
import { Loader2 } from 'lucide-react';

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

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      const user = setup ? await api.setup({ name, email, password }) : await api.login({ email, password });
      onAuthenticated(user);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
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
