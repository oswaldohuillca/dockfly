import { CircleCheck, CircleDashed, CircleSlash, CircleX, Loader2 } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import type { BuildStatus } from '@/lib/api';
import { cn } from '@/lib/utils';

const styles: Record<BuildStatus, { label: string; icon: typeof CircleCheck; className: string }> = {
  queued: { label: 'En cola', icon: CircleDashed, className: 'text-muted-foreground' },
  running: { label: 'Construyendo', icon: Loader2, className: 'text-blue-600 dark:text-blue-400 [&_svg]:animate-spin' },
  success: { label: 'Publicado', icon: CircleCheck, className: 'text-emerald-600 dark:text-emerald-400' },
  failed: { label: 'Fallido', icon: CircleX, className: 'text-destructive' },
  canceled: { label: 'Cancelado', icon: CircleSlash, className: 'text-muted-foreground' },
};

export function BuildStatusBadge({ status }: { status: BuildStatus }) {
  const s = styles[status];
  return (
    <Badge variant="outline" className={cn('gap-1', s.className)}>
      <s.icon /> {s.label}
    </Badge>
  );
}
