import { useLingui } from '@lingui/react/macro';

import { CheckField } from '@/components/check-field';

interface SSLValue {
  ssl: boolean;
  force_https: boolean;
  hsts: boolean;
}

interface Props<T extends SSLValue> {
  value: T;
  onChange: (key: keyof SSLValue, checked: boolean) => void;
}

/** Opciones de SSL comunes a proxy hosts y sitios. */
export function SSLFields<T extends SSLValue>({ value, onChange }: Props<T>) {
  const { t } = useLingui();
  return (
    <div className="space-y-4">
      <CheckField
        id="ssl"
        checked={value.ssl}
        onChange={(v) => onChange('ssl', v)}
        label={t`Get an SSL certificate from Let's Encrypt`}
        hint={t`Domains must point to this server (DNS A/AAAA record) and ports 80 and 443 must be open. Traefik issues and renews the certificate automatically.`}
      />
      <CheckField
        id="force-https"
        checked={value.force_https}
        onChange={(v) => onChange('force_https', v)}
        disabled={!value.ssl}
        label={t`Force HTTPS`}
        hint={t`Redirects HTTP → HTTPS.`}
      />
      <CheckField
        id="hsts"
        checked={value.hsts}
        onChange={(v) => onChange('hsts', v)}
        disabled={!value.ssl}
        label={t`Enable HSTS`}
        hint={t`Browsers will remember to always use HTTPS for one year.`}
      />
    </div>
  );
}
