import { CircleAlert, Clock, Lock, LockOpen, TriangleAlert } from 'lucide-react';
import { plural, t } from '@lingui/core/macro';
import { Trans } from '@lingui/react/macro';

import { Badge } from '@/components/ui/badge';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import type { CertState, CertStatus } from '@/lib/api';
import { currentLocale } from '@/lib/i18n';
import { cn } from '@/lib/utils';

// De peor a mejor: con varios dominios se muestra el peor.
const severity: CertState[] = ['error', 'expired', 'expiring', 'pending', 'valid'];

const shortDate = (iso: string) => new Date(iso).toLocaleDateString(currentLocale(), { day: 'numeric', month: 'short' });
const daysLeft = (iso: string) => Math.max(0, Math.ceil((new Date(iso).getTime() - Date.now()) / 86_400_000));

const look: Record<CertState, { icon: typeof Lock; className: string; label: (c: CertStatus) => string }> = {
  valid: {
    icon: Lock,
    className: 'text-emerald-600 dark:text-emerald-400',
    label: (c) => {
      const date = shortDate(c.not_after!);
      return t`Valid until ${date}`;
    },
  },
  expiring: {
    icon: Clock,
    className: 'text-amber-600 dark:text-amber-400',
    label: (c) => {
      const days = daysLeft(c.not_after!);
      return t`Expires in ${plural(days, { one: '# day', other: '# days' })}`;
    },
  },
  expired: { icon: LockOpen, className: 'text-destructive', label: () => t`Expired` },
  error: { icon: CircleAlert, className: 'text-destructive', label: () => t`SSL error` },
  pending: { icon: Clock, className: 'text-muted-foreground', label: () => t`Issuing certificate…` },
};

interface Props {
  domains: string[];
  certs: Map<string, CertStatus>;
}

/** Estado del certificado de un host o sitio con SSL. */
export function CertBadge({ domains, certs }: Props) {
  const list = domains.map((d) => certs.get(d)).filter((c): c is CertStatus => c !== undefined);
  if (list.length === 0) {
    return (
      <Badge variant="secondary" className="gap-1 text-muted-foreground">
        <Lock /> Let&apos;s Encrypt
      </Badge>
    );
  }
  const worst = list.reduce((a, b) => (severity.indexOf(b.status) < severity.indexOf(a.status) ? b : a));
  const s = look[worst.status];
  // Certificado vigente pero renovación fallando: se avisa sin alarmar.
  const renewalFailing = list.some((c) => c.status === 'valid' && c.error);
  const staging = list.some((c) => c.staging);

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span>
          <Badge variant="secondary" className={cn('gap-1', s.className)}>
            {renewalFailing && worst.status === 'valid' ? <TriangleAlert className="text-amber-500" /> : <s.icon />}
            {s.label(worst)}
            {staging && <span className="font-normal opacity-70">
                <Trans>(staging)</Trans>
              </span>}
          </Badge>
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-sm space-y-2 text-left">
        {list.map((c) => (
          <div key={c.domain} className="space-y-0.5">
            <div className="font-medium">
              {c.domain}: {look[c.status].label(c)}
            </div>
            {c.issuer && (
              <div className="opacity-80">
                <Trans>Issued by {c.issuer}</Trans>
                {c.staging && (
                  <>
                    {' — '}
                    <Trans>staging environment: browsers won't trust it</Trans>
                  </>
                )}
              </div>
            )}
            {c.hint && <div>{c.hint}</div>}
            {c.error && <div className="font-mono text-[11px] break-words opacity-70">{c.error}</div>}
          </div>
        ))}
      </TooltipContent>
    </Tooltip>
  );
}
