import { Loader2, RotateCw } from 'lucide-react';
import { Trans } from '@lingui/react/macro';

import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

interface Props {
  loading: boolean;
  error?: string;
  onRetry: () => void;
  className?: string;
}

/** The loading and load-failure states of a useResource; renders nothing once the data is ready. */
export function ResourceState({ loading, error, onRetry, className }: Props) {
  if (error)
    return (
      <div role="alert" className={cn('flex flex-col items-center gap-3 px-6 py-8 text-center text-sm', className)}>
        <div>
          <p className="font-medium">
            <Trans>Couldn't load this</Trans>
          </p>
          <p className="text-muted-foreground">{error}</p>
        </div>
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RotateCw /> <Trans>Retry</Trans>
        </Button>
      </div>
    );
  if (loading)
    return (
      <p className={cn('flex items-center justify-center gap-2 px-6 py-8 text-sm text-muted-foreground', className)}>
        <Loader2 className="size-4 animate-spin" /> <Trans>Loading…</Trans>
      </p>
    );
  return null;
}
