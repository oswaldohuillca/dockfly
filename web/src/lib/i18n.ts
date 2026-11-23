import { i18n, type Messages } from '@lingui/core';

export const locales = { en: 'English', es: 'Español' } as const;
export type Locale = keyof typeof locales;

const storageKey = 'dockfly.locale';

/** The language chosen in this browser; English by default. */
export function savedLocale(): Locale {
  const saved = localStorage.getItem(storageKey);
  return saved && saved in locales ? (saved as Locale) : 'en';
}

const loaders: Record<Locale, () => Promise<{ messages: Messages }>> = {
  en: () => import('../locales/en/messages.po'),
  es: () => import('../locales/es/messages.po'),
};

/** Loads and activates a language (each catalog is downloaded only when it is used). */
export async function activateLocale(locale: Locale) {
  const { messages } = await loaders[locale]();
  i18n.loadAndActivate({ locale, messages });
  document.documentElement.lang = locale;
  localStorage.setItem(storageKey, locale);
}

/** The active language, for Intl (dates, numbers…). */
export function currentLocale(): Locale {
  return (i18n.locale || 'en') as Locale;
}
