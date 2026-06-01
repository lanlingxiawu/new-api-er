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

export const LANGUAGE_PREFERENCE_KEY = 'appLanguagePreference';
export const LANGUAGE_FOLLOW_SYSTEM = 'system';

export const supportedLanguages = [
  'zh-CN',
  'zh-TW',
  'en',
  'fr',
  'ru',
  'ja',
  'vi',
];

export const languageOptions = [
  { key: 'zh-CN', shortLabel: 'CN', fullLabel: '\u7b80\u4f53\u4e2d\u6587' },
  { key: 'zh-TW', shortLabel: 'TW', fullLabel: '\u7e41\u9ad4\u4e2d\u6587' },
  { key: 'en', shortLabel: 'EN', fullLabel: 'English' },
  { key: 'fr', shortLabel: 'FR', fullLabel: 'Français' },
  { key: 'ru', shortLabel: 'RU', fullLabel: 'Русский' },
  { key: 'ja', shortLabel: 'JA', fullLabel: '日本語' },
  { key: 'vi', shortLabel: 'VI', fullLabel: 'Tiếng Việt' },
];

export const isFollowSystemLanguage = (language) =>
  !language || language === LANGUAGE_FOLLOW_SYSTEM;

export const normalizeLanguage = (language) => {
  if (!language) {
    return language;
  }

  const normalized = language.trim().replace(/_/g, '-');
  const lower = normalized.toLowerCase();

  if (
    lower === 'zh' ||
    lower === 'zh-cn' ||
    lower === 'zh-sg' ||
    lower.startsWith('zh-hans')
  ) {
    return 'zh-CN';
  }

  if (
    lower === 'zh-tw' ||
    lower === 'zh-hk' ||
    lower === 'zh-mo' ||
    lower.startsWith('zh-hant')
  ) {
    return 'zh-TW';
  }

  if (lower.startsWith('fr')) {
    return 'fr';
  }

  if (lower.startsWith('ru')) {
    return 'ru';
  }

  if (lower === 'jp' || lower.startsWith('ja')) {
    return 'ja';
  }

  if (lower.startsWith('vi')) {
    return 'vi';
  }

  const matchedLanguage = supportedLanguages.find(
    (supportedLanguage) => supportedLanguage.toLowerCase() === lower,
  );

  return matchedLanguage || 'zh-CN';
};

export const getSystemLanguage = () => {
  if (typeof navigator === 'undefined') {
    return 'zh-CN';
  }

  const candidates = [
    ...(Array.isArray(navigator.languages) ? navigator.languages : []),
    navigator.language,
    navigator.browserLanguage,
    navigator.userLanguage,
  ].filter(Boolean);

  for (const candidate of candidates) {
    const normalized = normalizeLanguage(candidate);
    if (normalized) {
      return normalized;
    }
  }

  return 'zh-CN';
};

export const resolveLanguagePreference = (language) => {
  if (isFollowSystemLanguage(language)) {
    return getSystemLanguage();
  }

  return normalizeLanguage(language);
};
