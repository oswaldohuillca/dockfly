import { Link } from '@tanstack/react-router';
import { Boxes, FileCode2, GitBranch, Globe, Languages, LogOut, Monitor, Moon, Server, Settings, Sun, UserRound } from 'lucide-react';
import { Trans, useLingui as useLinguiMacro } from '@lingui/react/macro';
import { useLingui } from '@lingui/react';
import { useTheme } from 'next-themes';

import { Logo } from '@/components/logo';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import type { Status, User } from '@/lib/api';
import { activateLocale, locales, type Locale } from '@/lib/i18n';
import { can } from '@/lib/roles';
import { cn } from '@/lib/utils';

interface Props {
  user: User;
  status: Status | null;
  onLogout: () => void;
}

export function Header({ user, status, onLogout }: Props) {
  const { theme, setTheme } = useTheme();
  const { i18n } = useLingui();
  const { t } = useLinguiMacro();

  const links = [
    { to: '/proxy-hosts', icon: Globe, label: 'Proxy Hosts' },
    { to: '/sites', icon: FileCode2, label: t`Sites` },
    { to: '/apps', icon: Boxes, label: t`Apps` },
    ...(can(user, 'admin')
      ? ([
          { to: '/servers', icon: Server, label: t`Servers` },
          { to: '/integrations', icon: GitBranch, label: t`Integrations` },
          { to: '/settings', icon: Settings, label: t`Settings` },
        ] as const)
      : []),
  ] as const;

  return (
    <>
      {/* Escritorio: barra lateral fija. */}
      <aside className="fixed inset-y-0 left-0 z-40 hidden w-64 flex-col bg-card shadow-card lg:flex">
        <Link to="/" className="mx-6 mt-7 mb-8 w-fit rounded-md">
          <Logo className="text-xl" />
        </Link>
        <nav aria-label={t`Main`} className="flex flex-1 flex-col gap-1 overflow-y-auto px-4">
          {links.map((l) => (
            <Link
              key={l.to}
              to={l.to}
              className="flex items-center gap-3 rounded-xl px-3 py-2.5 text-sm font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
              activeProps={{
                className: '!bg-primary !text-primary-foreground shadow-md shadow-primary/30',
              }}
            >
              <l.icon className="size-[1.125rem]" /> {l.label}
            </Link>
          ))}
        </nav>
        {status && (
          <div className="m-4 space-y-2 rounded-xl bg-muted/60 p-4 text-xs">
            <p className="font-medium text-muted-foreground">
              <Trans>System</Trans>
            </p>
            <StatusLine ok={status.traefik.running} detail={status.traefik.last_error}>
              {status.traefik.running ? <Trans>Traefik running</Trans> : <Trans>Traefik stopped</Trans>}
            </StatusLine>
            <StatusLine ok={status.docker_available}>
              {status.docker_available ? <Trans>Docker connected</Trans> : <Trans>Docker unavailable</Trans>}
            </StatusLine>
            <p className="pt-1 text-muted-foreground">Dockfly {status.version}</p>
          </div>
        )}
      </aside>

      <header className="sticky top-0 z-30 bg-background/80 backdrop-blur-md">
        <div className="mx-auto flex h-16 max-w-6xl items-center justify-between gap-3 px-4 lg:px-8">
          {/* Móvil y tablet: el logo y la navegación van arriba. */}
          <div className="flex min-w-0 items-center gap-3 lg:hidden">
            <Link to="/" className="shrink-0 rounded-md">
              <Logo className="text-lg" />
            </Link>
          </div>
          <div className="hidden lg:block" />

          <div className="flex shrink-0 items-center gap-2">
            {status && (
              <div className="flex items-center gap-2 lg:hidden">
                <StatusBadge ok={status.traefik.running}>
                  {status.traefik.running ? <Trans>Traefik running</Trans> : <Trans>Traefik stopped</Trans>}
                </StatusBadge>
                <StatusBadge ok={status.docker_available}>
                  {status.docker_available ? <Trans>Docker connected</Trans> : <Trans>Docker unavailable</Trans>}
                </StatusBadge>
              </div>
            )}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" className="ml-1 rounded-none border-transparent pl-1.5 shadow-card">
                <span className="grid size-6.5 place-items-center rounded-none bg-primary text-xs text-primary-foreground">
                  {user.name.charAt(0).toUpperCase()}
                </span>
                <span className="hidden sm:inline">{user.name}</span>
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-52">
              <DropdownMenuLabel className="font-normal">
                <div className="font-medium">{user.name}</div>
                <div className="text-xs text-muted-foreground">{user.email}</div>
              </DropdownMenuLabel>
              <DropdownMenuSeparator />
              <DropdownMenuLabel className="text-xs text-muted-foreground">
                <Trans>Theme</Trans>
              </DropdownMenuLabel>
              <DropdownMenuRadioGroup value={theme} onValueChange={setTheme}>
                <DropdownMenuRadioItem value="light">
                  <Sun /> <Trans>Light</Trans>
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="dark">
                  <Moon /> <Trans>Dark</Trans>
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="system">
                  <Monitor /> <Trans>System</Trans>
                </DropdownMenuRadioItem>
              </DropdownMenuRadioGroup>
              <DropdownMenuSeparator />
              <DropdownMenuLabel className="flex items-center gap-1.5 text-xs text-muted-foreground">
                <Languages className="size-3.5" /> <Trans>Language</Trans>
              </DropdownMenuLabel>
              <DropdownMenuRadioGroup value={i18n.locale} onValueChange={(v) => activateLocale(v as Locale)}>
                {Object.entries(locales).map(([code, name]) => (
                  <DropdownMenuRadioItem key={code} value={code}>
                    {name}
                  </DropdownMenuRadioItem>
                ))}
              </DropdownMenuRadioGroup>
              <DropdownMenuSeparator />
              <DropdownMenuItem asChild>
                <Link to="/account">
                  <UserRound /> <Trans>My account</Trans>
                </Link>
              </DropdownMenuItem>
              <DropdownMenuItem onClick={onLogout}>
                <LogOut /> <Trans>Log out</Trans>
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          </div>
        </div>
        <nav aria-label={t`Main`} className="flex gap-1 overflow-x-auto px-4 pb-3 lg:hidden">
          {links.map((l) => (
            <Link
              key={l.to}
              to={l.to}
              className="shrink-0 flex items-center gap-1.5 rounded-none px-3.5 py-2 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground"
              activeProps={{ className: '!bg-primary !text-primary-foreground' }}
            >
              <l.icon className="size-4" /> {l.label}
            </Link>
          ))}
        </nav>
      </header>
    </>
  );
}

function StatusLine({ ok, detail, children }: { ok: boolean; detail?: string; children: React.ReactNode }) {
  return (
    <div className={cn('flex items-start gap-2', !ok && 'text-destructive')}>
      <span className={cn('mt-1 size-2 shrink-0 rounded-none', ok ? 'bg-emerald-500 ring-3 ring-emerald-500/20' : 'bg-red-500 ring-3 ring-red-500/20')} />
      <span className="min-w-0">
        <span className="font-medium">{children}</span>
        {detail && <span className="block break-words text-muted-foreground">{detail}</span>}
      </span>
    </div>
  );
}

function StatusBadge({ ok, children }: { ok: boolean; children: React.ReactNode }) {
  return (
    <Badge variant="outline" className={cn('gap-1.5 bg-background', !ok && 'border-destructive/40 text-destructive')}>
      <span className={cn('size-1.5 rounded-none', ok ? 'bg-emerald-500' : 'bg-red-500')} />
      {/* En pantallas estrechas queda solo el punto; el texto sigue para lectores de pantalla. */}
      <span className="sr-only sm:not-sr-only">{children}</span>
    </Badge>
  );
}
