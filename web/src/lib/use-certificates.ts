import { useEffect, useState } from 'react';

import { api, type CertStatus } from '@/lib/api';

/**
 * Estado de los certificados por dominio. Se refresca cada 30 s: tras crear
 * un host, Let's Encrypt tarda unos segundos en responder.
 */
export function useCertificates(): Map<string, CertStatus> {
  const [certs, setCerts] = useState(new Map<string, CertStatus>());
  useEffect(() => {
    let active = true;
    const load = () =>
      api
        .certificates()
        .then((list) => active && setCerts(new Map(list.map((c) => [c.domain, c]))))
        .catch(() => {});
    load();
    const t = setInterval(load, 30_000);
    return () => {
      active = false;
      clearInterval(t);
    };
  }, []);
  return certs;
}
