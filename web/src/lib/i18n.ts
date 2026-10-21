import { i18n, type Messages } from '@lingui/core';

export const locales = { en: 'English', es: 'Español' } as const;
export type Locale = keyof typeof locales;

const storageKey = 'dockfly.locale';

/** El idioma elegido en este navegador; inglés por defecto. */
export function savedLocale(): Locale {
  const saved = localStorage.getItem(storageKey);
  return saved && saved in locales ? (saved as Locale) : 'en';
}

const loaders: Record<Locale, () => Promise<{ messages: Messages }>> = {
  en: () => import('../locales/en/messages.po'),
  es: () => import('../locales/es/messages.po'),
};

/** Carga y activa un idioma (cada catálogo se descarga solo cuando se usa). */
export async function activateLocale(locale: Locale) {
  const { messages } = await loaders[locale]();
  i18n.loadAndActivate({ locale, messages });
  document.documentElement.lang = locale;
  localStorage.setItem(storageKey, locale);
}

/** Idioma activo, para Intl (fechas, números…). */
export function currentLocale(): Locale {
  return (i18n.locale || 'en') as Locale;
}
