import type { ReactNode } from 'react';
import type { LucideIcon } from 'lucide-react';

import { cn } from '@/lib/utils';

/** Título de página, fuera de las tarjetas, con la acción principal a la derecha. */
export function PageHeader({ title, description, children }: { title: ReactNode; description?: ReactNode; children?: ReactNode }) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-x-6 gap-y-3">
      <div className="min-w-0">
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {description && <p className="mt-1 max-w-2xl text-sm text-muted-foreground">{description}</p>}
      </div>
      {children && <div className="flex flex-wrap items-center gap-2">{children}</div>}
    </div>
  );
}

const tones = {
  primary: 'bg-primary/10 text-primary',
  accent: 'bg-highlight/20 text-highlight-foreground',
  success: 'bg-emerald-500/12 text-emerald-700 dark:text-emerald-400',
  warning: 'bg-amber-500/15 text-amber-700 dark:text-amber-400',
  danger: 'bg-destructive/10 text-destructive',
  neutral: 'bg-muted text-muted-foreground',
};

export function StatGrid({ children }: { children: ReactNode }) {
  return <dl className="mb-6 grid grid-cols-2 gap-4 lg:grid-cols-4">{children}</dl>;
}

/** Cifra destacada con su etiqueta. */
export function Stat({ label, value, icon: Icon, tone = 'primary' }: { label: ReactNode; value: ReactNode; icon: LucideIcon; tone?: keyof typeof tones }) {
  return (
    <div className="flex items-center gap-4 rounded-2xl bg-card p-5 shadow-card">
      <div className={cn('grid size-11 shrink-0 place-items-center rounded-xl', tones[tone])}>
        <Icon className="size-5" />
      </div>
      <div className="min-w-0">
        <dt className="truncate text-xs font-medium text-muted-foreground">{label}</dt>
        <dd className="text-2xl leading-tight font-semibold tracking-tight tabular-nums">{value}</dd>
      </div>
    </div>
  );
}
