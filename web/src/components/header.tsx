import { Link } from '@tanstack/react-router';
import { Languages, LogOut, Monitor, Moon, Sun, UserRound } from 'lucide-react';
import { Trans } from '@lingui/react/macro';
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
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
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

  return (
    <header className="border-b bg-background">
      <div className="mx-auto flex h-14 max-w-6xl items-center justify-between px-4">
        <div className="flex items-center gap-6">
          <Link to="/">
            <Logo className="text-lg" />
          </Link>
          <nav className="flex gap-1">
            <NavLink to="/proxy-hosts">Proxy Hosts</NavLink>
            <NavLink to="/sites">
              <Trans>Sites</Trans>
            </NavLink>
            <NavLink to="/apps">
              <Trans>Apps</Trans>
            </NavLink>
            {can(user, 'admin') && (
              <>
                <NavLink to="/servers">
                  <Trans>Servers</Trans>
                </NavLink>
                <NavLink to="/integrations">
                  <Trans>Integrations</Trans>
                </NavLink>
                <NavLink to="/settings">
                  <Trans>Settings</Trans>
                </NavLink>
              </>
            )}
          </nav>
        </div>

        <div className="flex items-center gap-2">
          {status && (
            <>
              <Tooltip>
                <TooltipTrigger asChild>
                  <span>
                    <StatusBadge ok={status.traefik.running}>
                      {status.traefik.running ? <Trans>Traefik running</Trans> : <Trans>Traefik stopped</Trans>}
                    </StatusBadge>
                  </span>
                </TooltipTrigger>
                {status.traefik.last_error && <TooltipContent>{status.traefik.last_error}</TooltipContent>}
              </Tooltip>
              <StatusBadge ok={status.docker_available}>
                {status.docker_available ? <Trans>Docker connected</Trans> : <Trans>Docker unavailable</Trans>}
              </StatusBadge>
            </>
          )}

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="sm" className="ml-1">
                <span className="grid size-6 place-items-center rounded-full bg-primary text-xs text-primary-foreground">
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
    </header>
  );
}

function StatusBadge({ ok, children }: { ok: boolean; children: React.ReactNode }) {
  return (
    <Badge variant="outline" className="gap-1.5">
      <span className={cn('size-1.5 rounded-full', ok ? 'bg-emerald-500' : 'bg-red-500')} />
      {children}
    </Badge>
  );
}

function NavLink({
  to,
  children,
}: {
  to: '/proxy-hosts' | '/sites' | '/apps' | '/servers' | '/integrations' | '/settings';
  children: React.ReactNode;
}) {
  // activeProps se aplica también en las subrutas (/sites/3 marca «Sites»).
  return (
    <Link
      to={to}
      className="rounded-md px-3 py-1.5 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground"
      activeProps={{ className: 'bg-muted !text-foreground' }}
    >
      {children}
    </Link>
  );
}
