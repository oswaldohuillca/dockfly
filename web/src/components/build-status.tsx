import { CircleCheck, CircleDashed, CircleSlash, CircleX, Loader2 } from 'lucide-react';
import { msg } from '@lingui/core/macro';
import type { MessageDescriptor } from '@lingui/core';
import { useLingui } from '@lingui/react';

import { Badge } from '@/components/ui/badge';
import type { BuildStatus } from '@/lib/api';
import { cn } from '@/lib/utils';

const styles: Record<BuildStatus, { label: MessageDescriptor; icon: typeof CircleCheck; className: string }> = {
  queued: { label: msg`Queued`, icon: CircleDashed, className: 'text-muted-foreground' },
  running: { label: msg`Building`, icon: Loader2, className: 'text-blue-600 dark:text-blue-400 [&_svg]:animate-spin' },
  success: { label: msg`Published`, icon: CircleCheck, className: 'text-emerald-600 dark:text-emerald-400' },
  failed: { label: msg`Failed`, icon: CircleX, className: 'text-destructive' },
  canceled: { label: msg`Canceled`, icon: CircleSlash, className: 'text-muted-foreground' },
};

export function BuildStatusBadge({ status }: { status: BuildStatus }) {
  const { i18n } = useLingui();
  const s = styles[status];
  return (
    <Badge variant="outline" className={cn('gap-1', s.className)}>
      <s.icon /> {i18n._(s.label)}
    </Badge>
  );
}
