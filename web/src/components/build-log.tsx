import { useState } from 'react';
import { GitCommitHorizontal, Square } from 'lucide-react';
import { toast } from 'sonner';
import { Trans, useLingui } from '@lingui/react/macro';

import { BuildStatusBadge } from '@/components/build-status';
import { LiveLog } from '@/components/live-log';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { api, buildFinished, errorMessage, type Build } from '@/lib/api';
import { formatDate } from '@/lib/format';

interface Props {
  build: Build;
  onClose: () => void;
  /** Se llama cuando el build cambia de estado (p. ej. termina). */
  onChange: () => void;
}

export function BuildLog({ build: initial, onClose, onChange }: Props) {
  const { t } = useLingui();
  const [build, setBuild] = useState(initial);

  async function cancel() {
    try {
      await api.cancelBuild(build.id);
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex max-h-[90svh] flex-col sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-3">
            <Trans>Build #{build.id}</Trans> <BuildStatusBadge status={build.status} />
          </DialogTitle>
          <DialogDescription className="flex flex-wrap items-center gap-x-3 gap-y-1">
            {build.commit_sha && (
              <span className="flex items-center gap-1">
                <GitCommitHorizontal className="size-4" />
                <code className="font-mono">{build.commit_sha.slice(0, 7)}</code>
                {build.commit_message}
              </span>
            )}
            <span>
              {build.branch} · {build.trigger === 'push' ? t`push` : t`manual`} · {formatDate(build.created_at)}
            </span>
          </DialogDescription>
        </DialogHeader>

        <LiveLog
          url={api.buildLogsURL(initial.id)}
          onStatus={(b) => setBuild(b as Build)}
          onEnd={(b) => {
            setBuild(b as Build);
            onChange();
          }}
          emptyText={build.status === 'queued' ? t`Waiting in queue…` : undefined}
        />

        {build.error && build.status === 'failed' && <p className="text-sm text-destructive">{build.error}</p>}
        {!buildFinished(build) && (
          <div className="flex justify-end">
            <Button variant="outline" onClick={cancel}>
              <Square /> <Trans>Cancel build</Trans>
            </Button>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
