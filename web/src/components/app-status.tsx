import { CircleCheck, CircleDashed, CircleSlash, CircleX, History, Loader2 } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import type { App, AppDeployStatus } from '@/lib/api';
import { cn } from '@/lib/utils';

const deploy: Record<AppDeployStatus, { label: string; icon: typeof CircleCheck; className: string }> = {
  queued: { label: 'En cola', icon: CircleDashed, className: 'text-muted-foreground' },
  running: { label: 'Desplegando', icon: Loader2, className: 'text-blue-600 dark:text-blue-400 [&_svg]:animate-spin' },
  live: { label: 'Activo', icon: CircleCheck, className: 'text-emerald-600 dark:text-emerald-400' },
  replaced: { label: 'Sustituido', icon: History, className: 'text-muted-foreground' },
  failed: { label: 'Fallido', icon: CircleX, className: 'text-destructive' },
  canceled: { label: 'Cancelado', icon: CircleSlash, className: 'text-muted-foreground' },
};

export function DeployStatusBadge({ status }: { status: AppDeployStatus }) {
  const s = deploy[status];
  return (
    <Badge variant="outline" className={cn('gap-1', s.className)}>
      <s.icon /> {s.label}
    </Badge>
  );
}

/** Estado real del contenedor de una app. */
export function AppStateDot({ app }: { app: App }) {
  const { label, color } = appState(app);
  return (
    <span className="flex items-center gap-1.5 text-sm">
      <span className={cn('size-2 rounded-full', color)} />
      {label}
    </span>
  );
}

export function appState(app: App): { label: string; color: string } {
  if (!app.current) return { label: 'Sin desplegar', color: 'bg-muted-foreground/40' };
  if (app.stopped) return { label: 'Detenida', color: 'bg-muted-foreground' };
  switch (app.state) {
    case 'offline':
      return { label: 'Servidor desconectado', color: 'bg-amber-500' };
    case 'running':
      return { label: 'En marcha', color: 'bg-emerald-500' };
    case 'restarting':
      return { label: 'Reiniciándose en bucle', color: 'bg-amber-500' };
    case 'missing':
      return { label: 'Contenedor no encontrado', color: 'bg-red-500' };
    default:
      return { label: `Caída (${app.state || 'desconocido'})`, color: 'bg-red-500' };
  }
}
