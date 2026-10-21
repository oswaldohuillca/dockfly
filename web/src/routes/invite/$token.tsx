import { useState } from 'react';
import { createFileRoute, Link, useRouter } from '@tanstack/react-router';
import { Loader2 } from 'lucide-react';
import { Trans, useLingui } from '@lingui/react/macro';
import { toast } from 'sonner';

import { Logo } from '@/components/logo';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api, errorMessage } from '@/lib/api';
import { setUser } from '@/lib/session';

// Enlace de invitación (o de restablecer la contraseña): elegir contraseña y entrar.
export const Route = createFileRoute('/invite/$token')({
  loader: ({ params }) => api.getInvite(params.token).catch((e) => ({ error: errorMessage(e) })),
  component: Invite,
});

function Invite() {
  const { t } = useLingui();
  const data = Route.useLoaderData();
  const { token } = Route.useParams();
  const router = useRouter();
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    if (password !== confirm) {
      setError(t`Passwords don't match`);
      return;
    }
    setBusy(true);
    try {
      const res = await api.acceptInvite(token, password);
      if ('two_factor' in res) {
        // Con verificación en dos pasos, el código se pide al iniciar sesión.
        toast.success(t`Password saved: log in with it`);
        router.history.push('/login');
        return;
      }
      setUser(res);
      router.history.push('/');
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
          {'error' in data ? (
            <CardHeader>
              <CardTitle>
                <Trans>Invalid link</Trans>
              </CardTitle>
              <CardDescription>{data.error}</CardDescription>
              <Button variant="link" className="justify-start px-0" asChild>
                <Link to="/login" search={{}}>
                  <Trans>Go to login</Trans>
                </Link>
              </Button>
            </CardHeader>
          ) : (
            <>
              <CardHeader>
                <CardTitle>{data.new ? <Trans>Hi, {data.name}</Trans> : <Trans>New password</Trans>}</CardTitle>
                <CardDescription>
                  {data.new ? (
                    <Trans>You've been invited to Dockfly. Choose a password for {data.email}.</Trans>
                  ) : (
                    <Trans>Choose a new password for {data.email}.</Trans>
                  )}
                </CardDescription>
              </CardHeader>
              <CardContent>
                <form onSubmit={submit} className="space-y-4">
                  <input type="email" value={data.email} autoComplete="username" readOnly hidden />
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
                      minLength={8}
                      autoFocus
                      autoComplete="new-password"
                    />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="confirm">
                      <Trans>Confirm password</Trans>
                    </Label>
                    <Input
                      id="confirm"
                      type="password"
                      value={confirm}
                      onChange={(e) => setConfirm(e.target.value)}
                      required
                      minLength={8}
                      autoComplete="new-password"
                    />
                  </div>
                  {error && (
                    <Alert variant="destructive">
                      <AlertDescription>{error}</AlertDescription>
                    </Alert>
                  )}
                  <Button type="submit" className="w-full" disabled={busy}>
                    {busy && <Loader2 className="animate-spin" />}
                    {data.new ? <Trans>Create my account</Trans> : <Trans>Save and log in</Trans>}
                  </Button>
                </form>
              </CardContent>
            </>
          )}
        </Card>
      </div>
    </div>
  );
}
