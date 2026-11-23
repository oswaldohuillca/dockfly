import { useCallback, useRef, useState, type ReactNode } from 'react';
import { Trans } from '@lingui/react/macro';

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';

interface ConfirmOptions {
  title: ReactNode;
  description?: ReactNode;
  /** The button's text (Delete by default). */
  action?: ReactNode;
  /** Red button (yes by default). */
  destructive?: boolean;
}

/**
 * Confirmation before an action: `if (await confirm({ title })) …`.
 * The returned element (the dialog) is rendered once inside the component.
 */
export function useConfirm() {
  // The options are kept on close, so the dialog fades out with its text still in place.
  const [opts, setOpts] = useState<ConfirmOptions | null>(null);
  const [open, setOpen] = useState(false);
  const resolve = useRef<(ok: boolean) => void>(() => {});

  const confirm = useCallback(
    (o: ConfirmOptions) =>
      new Promise<boolean>((r) => {
        resolve.current = r;
        setOpts(o);
        setOpen(true);
      }),
    [],
  );
  const close = (ok: boolean) => {
    resolve.current(ok);
    setOpen(false);
  };

  const dialog = (
    <AlertDialog open={open} onOpenChange={(open) => !open && close(false)}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{opts?.title}</AlertDialogTitle>
          {opts?.description && <AlertDialogDescription>{opts.description}</AlertDialogDescription>}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>
            <Trans>Cancel</Trans>
          </AlertDialogCancel>
          <AlertDialogAction variant={opts?.destructive === false ? 'default' : 'destructive'} onClick={() => close(true)}>
            {opts?.action ?? <Trans>Delete</Trans>}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
  return { confirm, dialog };
}
