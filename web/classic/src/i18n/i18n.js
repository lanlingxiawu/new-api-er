/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import LanguageDetector from 'i18next-browser-languagedetector';

import enTranslation from './locales/en.json';
import frTranslation from './locales/fr.json';
import zhCNTranslation from './locales/zh-CN.json';
import zhTWTranslation from './locales/zh-TW.json';
import ruTranslation from './locales/ru.json';
import jaTranslation from './locales/ja.json';
import viTranslation from './locales/vi.json';
import {
  getSystemLanguage,
  normalizeLanguage,
  supportedLanguages,
} from './language';

const detectionOptions = {
  order: ['querystring', 'localStorage', 'navigator', 'htmlTag'],
  caches: ['localStorage'],
  lookupLocalStorage: 'i18nextLng',
  convertDetectedLanguage: (lng) => normalizeLanguage(lng),
};

const initialLanguage =
  normalizeLanguage(
    typeof window !== 'undefined'
      ? window.localStorage?.getItem('i18nextLng')
      : null,
  ) || getSystemLanguage();

i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    lng: initialLanguage,
    load: 'currentOnly',
    // NOTE: do NOT enable nonExplicitSupportedLngs here. With it on, i18next
    // strips region codes for the supportedLngs check (zh-CN -> zh); since
    // supportedLanguages lists only 'zh-CN'/'zh-TW' (no base 'zh'), Chinese
    // would be rejected, the resolve hierarchy becomes empty, and every
    // English-source key falls back to its key (renders English) for zh users.
    // Detected languages are already canonicalized by convertDetectedLanguage
    // (normalizeLanguage), so explicit matching against supportedLanguages is
    // both sufficient and correct.
    supportedLngs: supportedLanguages,
    detection: detectionOptions,
    resources: {
      en: enTranslation,
      zh: zhCNTranslation,
      'zh-CN': zhCNTranslation,
      'zh-Hans': zhCNTranslation,
      'zh-TW': zhTWTranslation,
      'zh-Hant': zhTWTranslation,
      fr: frTranslation,
      ru: ruTranslation,
      ja: jaTranslation,
      vi: viTranslation,
    },
    fallbackLng: 'zh-CN',
    nsSeparator: false,
    interpolation: {
      escapeValue: false,
    },
  });

window.__i18n = i18n;

export default i18n;
