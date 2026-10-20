import { useCallback, useEffect, useState } from 'react';
import { ThemeProvider } from 'next-themes';

import { AuthForm } from '@/components/auth-form';
import { Header } from '@/components/header';
import { ProxyHosts } from '@/components/proxy-hosts';
import { SiteDetail } from '@/components/site-detail';
import { Sites } from '@/components/sites';
import { Toaster } from '@/components/ui/sonner';
import { TooltipProvider } from '@/components/ui/tooltip';
import { api, ApiError, type Status, type User } from '@/lib/api';
import { useRoute } from '@/lib/router';

export default function App() {
  return (
    <ThemeProvider attribute="class" defaultTheme="system" enableSystem disableTransitionOnChange>
      <TooltipProvider>
        <Shell />
        <Toaster richColors position="bottom-right" />
      </TooltipProvider>
    </ThemeProvider>
  );
}

function Shell() {
  const route = useRoute();
  const [user, setUser] = useState<User | null>(null);
  const [needsSetup, setNeedsSetup] = useState(false);
  const [loading, setLoading] = useState(true);
  const [status, setStatus] = useState<Status | null>(null);

  useEffect(() => {
    (async () => {
      try {
        const s = await api.setupStatus();
        setNeedsSetup(s.needs_setup);
        if (!s.needs_setup) setUser(await api.me());
      } catch (e) {
        if (!(e instanceof ApiError && e.status === 401)) console.error(e);
      } finally {
        setLoading(false);
      }
    })();
  }, []);

  const refreshStatus = useCallback(async () => {
    try {
      setStatus(await api.status());
    } catch {
      setStatus(null);
    }
  }, []);

  useEffect(() => {
    if (!user) return;
    refreshStatus();
    const t = setInterval(refreshStatus, 10_000);
    return () => clearInterval(t);
  }, [user, refreshStatus]);

  async function logout() {
    await api.logout();
    setUser(null);
  }

  if (loading) {
    return <div className="grid min-h-svh place-items-center text-muted-foreground">Cargando…</div>;
  }

  if (!user) {
    return (
      <AuthForm
        setup={needsSetup}
        onAuthenticated={(u) => {
          setUser(u);
          setNeedsSetup(false);
        }}
      />
    );
  }

  return (
    <div className="flex min-h-svh flex-col bg-muted/40">
      <Header user={user} status={status} route={route} onLogout={logout} />
      <main className="mx-auto w-full max-w-6xl flex-1 px-4 py-6">
        <Page route={route} dockerAvailable={status?.docker_available ?? false} />
      </main>
      <footer className="py-4 text-center text-xs text-muted-foreground">Dockfly {status?.version}</footer>
    </div>
  );
}

function Page({ route, dockerAvailable }: { route: string; dockerAvailable: boolean }) {
  const site = route.match(/^\/sites\/(\d+)$/);
  if (site) return <SiteDetail key={site[1]} id={Number(site[1])} />;
  if (route.startsWith('/sites')) return <Sites />;
  return <ProxyHosts dockerAvailable={dockerAvailable} />;
}
