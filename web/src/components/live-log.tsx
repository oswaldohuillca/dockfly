import { useEffect, useRef, useState } from 'react';

const ansi = /\x1b\[[0-9;?]*[A-Za-z]/g;

interface Props {
  /** URL de un flujo SSE con eventos "log", "status" y "end". */
  url: string;
  onStatus?: (data: unknown) => void;
  onEnd?: (data: unknown) => void;
  emptyText?: string;
  className?: string;
}

/**
 * Log en vivo por SSE. Si la conexión se corta, vuelve a conectar desde cero
 * (el servidor reenvía el log completo), para no duplicar líneas.
 */
export function LiveLog({ url, onStatus, onEnd, emptyText = 'Cargando…', className }: Props) {
  const [text, setText] = useState('');
  const pre = useRef<HTMLPreElement>(null);
  const stick = useRef(true);
  const handlers = useRef({ onStatus, onEnd });
  handlers.current = { onStatus, onEnd };

  useEffect(() => {
    let es: EventSource | null = null;
    let retry: ReturnType<typeof setTimeout>;
    let closed = false;
    const connect = () => {
      setText('');
      es = new EventSource(url);
      es.addEventListener('log', (e) => setText((t) => t + (JSON.parse(e.data) as string).replace(ansi, '')));
      es.addEventListener('status', (e) => handlers.current.onStatus?.(JSON.parse(e.data)));
      es.addEventListener('end', (e) => {
        handlers.current.onEnd?.(JSON.parse(e.data));
        closed = true;
        es?.close();
      });
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
  }, [url]);

  useEffect(() => {
    if (pre.current && stick.current) pre.current.scrollTop = pre.current.scrollHeight;
  }, [text]);

  return (
    <pre
      ref={pre}
      onScroll={(e) => {
        const el = e.currentTarget;
        stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
      }}
      className={
        className ??
        'min-h-72 flex-1 overflow-auto rounded-md bg-zinc-950 p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap text-zinc-100'
      }
    >
      {text || emptyText}
    </pre>
  );
}
