// Ventana emergente para autorizar en GitHub/GitLab, como en Vercel o
// Netlify. La página de retorno de Dockfly avisa con postMessage y se cierra.

export interface PopupResult {
  ok: boolean;
  message: string;
}

const NAME = 'dockfly-oauth';

/**
 * Abre la ventana vacía. Debe llamarse directamente en el manejador del clic:
 * si se abre después de un await, el navegador la bloquea.
 */
export function openPopup(): Window | null {
  const w = 1000;
  const h = 760;
  const left = window.screenX + (window.outerWidth - w) / 2;
  const top = window.screenY + (window.outerHeight - h) / 2;
  return window.open('about:blank', NAME, `popup,width=${w},height=${h},left=${left},top=${top}`);
}

/** Lleva la ventana (o, si se bloqueó, esta misma pestaña) a url. */
export function goTo(popup: Window | null, url: string) {
  if (popup) popup.location.href = url;
  else window.location.href = url;
}

/**
 * Envía un formulario POST a la ventana (el manifiesto de la GitHub App se
 * envía así). Sin ventana, se envía en esta pestaña.
 */
export function postTo(popup: Window | null, action: string, fields: Record<string, string>) {
  const form = document.createElement('form');
  form.method = 'POST';
  form.action = action;
  form.target = popup ? NAME : '_self';
  for (const [name, value] of Object.entries(fields)) {
    const input = document.createElement('input');
    input.type = 'hidden';
    input.name = name;
    input.value = value;
    form.appendChild(input);
  }
  document.body.appendChild(form);
  form.submit();
  form.remove();
}

/** Espera el resultado. Si el usuario cierra la ventana, ok = false. */
export function waitForPopup(popup: Window | null): Promise<PopupResult> {
  if (!popup) return new Promise(() => {}); // la pestaña entera navegó fuera
  return new Promise((resolve) => {
    const onMessage = (e: MessageEvent) => {
      if (e.origin !== window.location.origin || e.data?.type !== 'dockfly-oauth') return;
      done({ ok: e.data.ok, message: e.data.message });
    };
    const timer = setInterval(() => {
      if (popup.closed) done({ ok: false, message: '' });
    }, 500);
    const done = (r: PopupResult) => {
      clearInterval(timer);
      window.removeEventListener('message', onMessage);
      resolve(r);
    };
    window.addEventListener('message', onMessage);
  });
}
