import { zh } from './zh';
import { en } from './en';
import type { Locale, Translation } from './types';

export type { Locale, Translation };

export const translations: Record<Locale, Translation> = {
  'zh-CN': zh,
  en,
};

export function useTranslations(locale: Locale): Translation {
  return translations[locale];
}

/** Pull locale from Astro url pathname. Defaults to zh-CN (default locale, no prefix). */
export function getLocale(pathname: string): Locale {
  if (pathname.startsWith('/en')) return 'en';
  return 'zh-CN';
}

export function pathFor(locale: Locale, path: string): string {
  const cleanPath = path.startsWith('/') ? path : `/${path}`;
  if (locale === 'en') return `/en${cleanPath === '/' ? '' : cleanPath}`;
  return cleanPath === '/' ? '/' : cleanPath;
}

export function alternateLocale(locale: Locale): Locale {
  return locale === 'en' ? 'zh-CN' : 'en';
}

export function alternateLabel(locale: Locale): string {
  return locale === 'en' ? '中文' : 'English';
}
