import { useEffect, useRef, useState } from 'react';
import { GitCommitHorizontal, Square } from 'lucide-react';
import { toast } from 'sonner';

import { BuildStatusBadge } from '@/components/build-status';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { api, buildFinished, errorMessage, type Build } from '@/lib/api';
import { formatDate } from '@/lib/format';

// Quita los códigos de color ANSI que algunas herramientas emiten aunque se
// les pida NO_COLOR.
const ansi = /\x1b\[[0-9;?]*[A-Za-z]/g;

interface Props {
  build: Build;
  onClose: () => void;
  /** Se llama cuando el build cambia de estado (p. ej. termina). */
  onChange: () => void;
}

export function BuildLog({ build: initial, onClose, onChange }: Props) {
  const [build, setBuild] = useState(initial);
  const [text, setText] = useState('');
  const pre = useRef<HTMLPreElement>(null);
  const stickToBottom = useRef(true);

  useEffect(() => {
    let es: EventSource | null = null;
    let retry: ReturnType<typeof setTimeout>;
    let closed = false;

    const connect = () => {
      // El servidor envía el log completo en cada conexión: empezamos de cero.
      setText('');
      es = new EventSource(api.buildLogsURL(initial.id));
      es.addEventListener('log', (e) => setText((t) => t + (JSON.parse(e.data) as string).replace(ansi, '')));
      es.addEventListener('status', (e) => setBuild(JSON.parse(e.data)));
      es.addEventListener('end', (e) => {
        setBuild(JSON.parse(e.data));
        es?.close();
        onChange();
      });
      // Sin esto, EventSource reconecta solo y el log se duplicaría.
      es.onerror = () => {
        es?.close();
        if (!closed) retry = setTimeout(connect, 2000);
      };
    };
    connect();
    return () => {
      closed = true;
      clearTimeout(retry);
      es?.close();
    };
  }, [initial.id, onChange]);

  // Seguir el final del log, salvo que el usuario haya subido para leer.
  useEffect(() => {
    const el = pre.current;
    if (el && stickToBottom.current) el.scrollTop = el.scrollHeight;
  }, [text]);

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
            Build #{build.id} <BuildStatusBadge status={build.status} />
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
              {build.branch} · {build.trigger === 'push' ? 'push' : 'manual'} · {formatDate(build.created_at)}
            </span>
          </DialogDescription>
        </DialogHeader>

        <pre
          ref={pre}
          onScroll={(e) => {
            const el = e.currentTarget;
            stickToBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
          }}
          className="min-h-72 flex-1 overflow-auto rounded-md bg-zinc-950 p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap text-zinc-100"
        >
          {text || (build.status === 'queued' ? 'Esperando turno en la cola…' : 'Cargando…')}
        </pre>

        {build.error && build.status === 'failed' && <p className="text-sm text-destructive">{build.error}</p>}
        {!buildFinished(build) && (
          <div className="flex justify-end">
            <Button variant="outline" onClick={cancel}>
              <Square /> Cancelar build
            </Button>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
