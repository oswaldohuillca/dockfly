import { useCallback, useEffect, useState } from 'react';
import { createFileRoute, Outlet, redirect, useNavigate } from '@tanstack/react-router';

import { Header } from '@/components/header';
import { api, type Status } from '@/lib/api';
import { getUser, setUser } from '@/lib/session';
import { StatusContext } from '@/lib/status';

// Layout de todo el panel. El "_" hace que no añada un segmento a la URL.
export const Route = createFileRoute('/_app')({
  beforeLoad: async ({ location }) => {
    const user = await getUser();
    if (!user) throw redirect({ to: '/login', search: { redirect: location.href } });
    return { user };
  },
  component: AppLayout,
});

function AppLayout() {
  const { user } = Route.useRouteContext();
  const navigate = useNavigate();
  const [status, setStatus] = useState<Status | null>(null);

  const refreshStatus = useCallback(async () => {
    setStatus(await api.status().catch(() => null));
  }, []);

  useEffect(() => {
    refreshStatus();
    const t = setInterval(refreshStatus, 10_000);
    return () => clearInterval(t);
  }, [refreshStatus]);

  async function logout() {
    await api.logout();
    setUser(null);
    navigate({ to: '/login' });
  }

  return (
    <StatusContext.Provider value={status}>
      <div className="flex min-h-svh flex-col bg-muted/40">
        <Header user={user} status={status} onLogout={logout} />
        <main className="mx-auto w-full max-w-6xl flex-1 px-4 py-6">
          <Outlet />
        </main>
        <footer className="py-4 text-center text-xs text-muted-foreground">Dockfly {status?.version}</footer>
      </div>
    </StatusContext.Provider>
  );
}
