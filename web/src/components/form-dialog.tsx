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
  /** Mensaje de error (ya traducido) bajo los campos. */
  error?: string;
  /** Texto del botón de enviar (por defecto, Save). */
  submitLabel?: ReactNode;
  /** Desactiva el botón de enviar (además de mientras busy). */
  submitDisabled?: boolean;
  /** Contenido extra del pie, a la izquierda de los botones. */
  footer?: ReactNode;
  className?: string;
  children: ReactNode;
}

/** Diálogo con un formulario: cabecera, campos, error y Cancel / Save. */
export function FormDialog({
  title, description, onClose, onSubmit, busy, error, submitLabel, submitDisabled, footer, className, children,
}: Props) {
  return (
    <Dialog open onOpenChange={(open) => !open && !busy && onClose()}>
      {/* Un clic fuera no cierra: se perdería lo escrito. */}
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
