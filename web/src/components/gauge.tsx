import type { ReactNode } from 'react';

import { cn } from '@/lib/utils';

interface Props {
  /** Qué mide: «CPU», «Memory»… (también es su nombre accesible). */
  label: string;
  /** De 0 a 100. */
  percent: number;
  /** Texto bajo el medidor: «3.1 GB of 8 GB». */
  detail?: ReactNode;
  className?: string;
}

// Arco de 270° abierto por abajo, con centro en (60, 60) y radio 50.
const ARC = 'M24.64 95.36 A50 50 0 1 1 95.36 95.36';

/** Medidor de uso en arco: pasa a ámbar desde el 75 % y a rojo desde el 90 %. */
export function Gauge({ label, percent, detail, className }: Props) {
  const value = Math.min(100, Math.max(0, percent));
  const tone = value >= 90 ? 'stroke-destructive' : value >= 75 ? 'stroke-amber-500' : 'stroke-primary';
  return (
    <div className={cn('flex flex-col items-center gap-1 text-center', className)}>
      <div
        role="meter"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(value)}
        aria-valuetext={`${Math.round(value)}%`}
        className="relative w-full max-w-36"
      >
        <svg viewBox="0 0 120 108" className="block w-full" aria-hidden="true">
          <path d={ARC} pathLength={100} className="fill-none stroke-muted" strokeWidth={10} />
          <path
            d={ARC}
            pathLength={100}
            className={cn('fill-none transition-[stroke-dasharray] duration-500 motion-reduce:transition-none', tone)}
            strokeWidth={10}
            strokeDasharray={`${value} 100`}
          />
        </svg>
        <div className="absolute inset-x-0 top-[38%] flex flex-col items-center">
          <span className="text-2xl leading-none font-semibold tracking-tight tabular-nums">
            {Math.round(value)}
            <span className="text-sm font-medium text-muted-foreground">%</span>
          </span>
          <span className="mt-1 text-xs font-medium text-muted-foreground">{label}</span>
        </div>
      </div>
      {detail && <div className="text-xs text-muted-foreground tabular-nums">{detail}</div>}
    </div>
  );
}
