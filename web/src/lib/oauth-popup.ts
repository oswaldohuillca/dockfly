// Popup window to authorize on GitHub/GitLab, the way Vercel or Netlify do
// it. Dockfly's return page reports back with postMessage and closes itself.

export interface PopupResult {
  ok: boolean;
  message: string;
}

const NAME = 'dockfly-oauth';

/**
 * Opens the empty window. It must be called straight from the click handler:
 * if it is opened after an await, the browser blocks it.
 */
export function openPopup(): Window | null {
  const w = 1000;
  const h = 760;
  const left = window.screenX + (window.outerWidth - w) / 2;
  const top = window.screenY + (window.outerHeight - h) / 2;
  return window.open('about:blank', NAME, `popup,width=${w},height=${h},left=${left},top=${top}`);
}

/** Sends the window (or, if it was blocked, this very tab) to url. */
export function goTo(popup: Window | null, url: string) {
  if (popup) popup.location.href = url;
  else window.location.href = url;
}

/**
 * Posts a form to the window (that is how the GitHub App manifest is sent).
 * With no window, it is submitted in this tab.
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

/** Waits for the result. If the user closes the window, ok = false. */
export function waitForPopup(popup: Window | null): Promise<PopupResult> {
  if (!popup) return new Promise(() => {}); // the whole tab navigated away
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
