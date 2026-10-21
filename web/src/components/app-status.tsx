import { msg, t } from '@lingui/core/macro';
import type { MessageDescriptor } from '@lingui/core';
import { useLingui } from '@lingui/react';
import { CircleCheck, CircleDashed, CircleSlash, CircleX, History, Loader2 } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import type { App, AppDeployStatus } from '@/lib/api';
import { cn } from '@/lib/utils';

const deploy: Record<AppDeployStatus, { label: MessageDescriptor; icon: typeof CircleCheck; className: string }> = {
  queued: { label: msg`Queued`, icon: CircleDashed, className: 'text-muted-foreground' },
  running: { label: msg`Deploying`, icon: Loader2, className: 'text-blue-600 dark:text-blue-400 [&_svg]:animate-spin' },
  live: { label: msg`Live`, icon: CircleCheck, className: 'text-emerald-600 dark:text-emerald-400' },
  replaced: { label: msg`Replaced`, icon: History, className: 'text-muted-foreground' },
  failed: { label: msg`Failed`, icon: CircleX, className: 'text-destructive' },
  canceled: { label: msg`Canceled`, icon: CircleSlash, className: 'text-muted-foreground' },
};

export function DeployStatusBadge({ status }: { status: AppDeployStatus }) {
  const { i18n } = useLingui();
  const s = deploy[status];
  return (
    <Badge variant="outline" className={cn('gap-1', s.className)}>
      <s.icon /> {i18n._(s.label)}
    </Badge>
  );
}

/** Estado real del contenedor de una app. */
export function AppStateDot({ app }: { app: App }) {
  useLingui(); // vuelve a renderizar al cambiar de idioma
  const { label, color } = appState(app);
  return (
    <span className="flex items-center gap-1.5 text-sm">
      <span className={cn('size-2 rounded-full', color)} />
      {label}
    </span>
  );
}

export function appState(app: App): { label: string; color: string } {
  if (!app.current) return { label: t`Not deployed`, color: 'bg-muted-foreground/40' };
  if (app.stopped) return { label: t`Stopped`, color: 'bg-muted-foreground' };
  switch (app.state) {
    case 'offline':
      return { label: t`Server offline`, color: 'bg-amber-500' };
    case 'running':
      return { label: t`Running`, color: 'bg-emerald-500' };
    case 'restarting':
      return { label: t`Restart loop`, color: 'bg-amber-500' };
    case 'missing':
      return { label: t`Container not found`, color: 'bg-red-500' };
    default: {
      const state = app.state || t`unknown`;
      return { label: t`Down (${state})`, color: 'bg-red-500' };
    }
  }
}
