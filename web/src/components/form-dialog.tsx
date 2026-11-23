import type { FormEvent, ReactNode } from 'react';
import { Loader2, TriangleAlert } from 'lucide-react';
import { Trans } from '@lingui/react/macro';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';

interface Props {
  title: ReactNode;
  description?: ReactNode;
  onClose: () => void;
  onSubmit: (e: FormEvent) => void;
  busy?: boolean;
  /** Error message (already translated) shown under the fields. */
  error?: string;
  /** The submit button's text (Save by default). */
  submitLabel?: ReactNode;
  /** Disables the submit button (on top of while busy). */
  submitDisabled?: boolean;
  /** Extra footer content, to the left of the buttons. */
  footer?: ReactNode;
  className?: string;
  children: ReactNode;
}

/** Dialog with a form: header, fields, error and Cancel / Save. */
export function FormDialog({
  title, description, onClose, onSubmit, busy, error, submitLabel, submitDisabled, footer, className, children,
}: Props) {
  return (
    <Dialog open onOpenChange={(open) => !open && !busy && onClose()}>
      {/* Clicking outside does not close it: whatever was typed would be lost. */}
      <DialogContent className={className} onInteractOutside={(e) => e.preventDefault()}>
        <form onSubmit={onSubmit} className="contents">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            {description && <DialogDescription>{description}</DialogDescription>}
          </DialogHeader>
          {children}
          {error && (
            <Alert variant="destructive">
              <TriangleAlert />
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            {footer}
            <Button type="button" variant="outline" onClick={onClose} disabled={busy}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={busy || submitDisabled}>
              {busy && <Loader2 className="animate-spin" />}
              {submitLabel ?? <Trans>Save</Trans>}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
