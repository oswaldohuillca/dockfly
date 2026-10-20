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
  return (
    <div className="space-y-4">
      <CheckField
        id="ssl"
        checked={value.ssl}
        onChange={(v) => onChange('ssl', v)}
        label="Obtener certificado SSL de Let's Encrypt"
        hint="Los dominios deben apuntar a este servidor (registro DNS A/AAAA) y los puertos 80 y 443 deben estar abiertos. Traefik obtiene y renueva el certificado automáticamente."
      />
      <CheckField
        id="force-https"
        checked={value.force_https}
        onChange={(v) => onChange('force_https', v)}
        disabled={!value.ssl}
        label="Forzar HTTPS"
        hint="Redirige HTTP → HTTPS."
      />
      <CheckField
        id="hsts"
        checked={value.hsts}
        onChange={(v) => onChange('hsts', v)}
        disabled={!value.ssl}
        label="Activar HSTS"
        hint="El navegador recordará usar siempre HTTPS durante un año."
      />
    </div>
  );
}
