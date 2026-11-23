import { useEffect, useState } from 'react';

import { api, type CertStatus } from '@/lib/api';

/**
 * Certificate status by domain. Refreshed every 30 s: after a host is
 * created, Let's Encrypt takes a few seconds to answer.
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
