import { Link } from '@tanstack/react-router';
import { LogOut, Monitor, Moon, Sun, UserRound } from 'lucide-react';
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
import { cn } from '@/lib/utils';

interface Props {
  user: User;
  status: Status | null;
  onLogout: () => void;
}

export function Header({ user, status, onLogout }: Props) {
  const { theme, setTheme } = useTheme();

  return (
    <header className="border-b bg-background">
      <div className="mx-auto flex h-14 max-w-6xl items-center justify-between px-4">
        <div className="flex items-center gap-6">
          <Link to="/">
            <Logo className="text-lg" />
          </Link>
          <nav className="flex gap-1">
            <NavLink to="/proxy-hosts">Proxy Hosts</NavLink>
            <NavLink to="/sites">Sitios</NavLink>
            <NavLink to="/apps">Apps</NavLink>
            <NavLink to="/integrations">Integraciones</NavLink>
            <NavLink to="/settings">Ajustes</NavLink>
          </nav>
        </div>

        <div className="flex items-center gap-2">
          {status && (
            <>
              <Tooltip>
                <TooltipTrigger asChild>
                  <span>
                    <StatusBadge ok={status.traefik.running}>
                      Traefik {status.traefik.running ? 'activo' : 'detenido'}
                    </StatusBadge>
                  </span>
                </TooltipTrigger>
                {status.traefik.last_error && <TooltipContent>{status.traefik.last_error}</TooltipContent>}
              </Tooltip>
              <StatusBadge ok={status.docker_available}>
                Docker {status.docker_available ? 'conectado' : 'no disponible'}
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
              <DropdownMenuLabel className="text-xs text-muted-foreground">Tema</DropdownMenuLabel>
              <DropdownMenuRadioGroup value={theme} onValueChange={setTheme}>
                <DropdownMenuRadioItem value="light">
                  <Sun /> Claro
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="dark">
                  <Moon /> Oscuro
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="system">
                  <Monitor /> Sistema
                </DropdownMenuRadioItem>
              </DropdownMenuRadioGroup>
              <DropdownMenuSeparator />
              <DropdownMenuItem asChild>
                <Link to="/account">
                  <UserRound /> Mi cuenta
                </Link>
              </DropdownMenuItem>
              <DropdownMenuItem onClick={onLogout}>
                <LogOut /> Cerrar sesión
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
  to: '/proxy-hosts' | '/sites' | '/apps' | '/integrations' | '/settings';
  children: React.ReactNode;
}) {
  // activeProps se aplica también en las subrutas (/sites/3 marca «Sitios»).
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
