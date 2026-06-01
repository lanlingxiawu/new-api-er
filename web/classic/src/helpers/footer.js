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

const createId = (prefix, index) => `${prefix}-${index + 1}`;

export const DEFAULT_FOOTER_CONFIG = {
  version: 1,
  groups: [
    {
      id: 'company',
      titleKey: '\u516c\u53f8',
      links: [
        {
          id: 'about',
          labelKey: '\u5173\u4e8e',
          url: '/about',
          target: '_self',
        },
        {
          id: 'browse-models',
          labelKey: '\u6d4f\u89c8\u6a21\u578b',
          url: '/pricing',
          target: '_self',
        },
        {
          id: 'how-it-works',
          labelKey: '\u5de5\u4f5c\u539f\u7406',
          url: 'https://docs.newapi.pro/wiki/features-introduction/',
          target: '_blank',
        },
        {
          id: 'blog',
          labelKey: '\u535a\u5ba2',
          url: 'https://github.com/QuantumNous/new-api',
          target: '_blank',
        },
      ],
    },
    {
      id: 'socials',
      titleKey: '\u793e\u4ea4',
      links: [
        {
          id: 'twitter-x',
          labelKey: 'Twitter / X',
          url: 'https://github.com/QuantumNous/new-api',
          target: '_blank',
        },
        {
          id: 'telegram',
          labelKey: 'Telegram',
          url: 'https://github.com/QuantumNous/new-api',
          target: '_blank',
        },
        {
          id: 'discord',
          labelKey: 'Discord',
          url: 'https://github.com/QuantumNous/new-api',
          target: '_blank',
        },
      ],
    },
    {
      id: 'legal',
      titleKey: '\u6cd5\u5f8b',
      links: [
        {
          id: 'whitepaper',
          labelKey: '\u767d\u76ae\u4e66',
          url: 'https://docs.newapi.pro/',
          target: '_blank',
        },
        {
          id: 'privacy-policy',
          labelKey: '\u9690\u79c1\u653f\u7b56',
          url: '/privacy-policy',
          target: '_self',
        },
      ],
    },
  ],
};
export function cloneFooterConfig(config = DEFAULT_FOOTER_CONFIG) {
  return structuredClone(config);
}

export function normalizeFooterConfig(config) {
  if (!config || typeof config !== 'object' || Array.isArray(config)) {
    return null;
  }

  const groups = Array.isArray(config.groups)
    ? config.groups
        .map((group, groupIndex) => {
          if (!group || typeof group !== 'object' || Array.isArray(group)) {
            return null;
          }

          const links = Array.isArray(group.links)
            ? group.links
                .map((link, linkIndex) => {
                  if (
                    !link ||
                    typeof link !== 'object' ||
                    Array.isArray(link)
                  ) {
                    return null;
                  }

                  const labelKey = String(
                    link.labelKey ?? link.label ?? '',
                  ).trim();
                  const url = String(link.url ?? '').trim();
                  if (!labelKey || !url) {
                    return null;
                  }

                  const rawTarget = String(link.target ?? '_blank').trim();
                  const target =
                    rawTarget === '_self' || rawTarget === '_blank'
                      ? rawTarget
                      : '_blank';

                  return {
                    id: String(link.id ?? createId('link', linkIndex)).trim(),
                    labelKey,
                    url,
                    target,
                  };
                })
                .filter(Boolean)
            : [];

          const id = String(group.id ?? createId('group', groupIndex)).trim();
          const titleKey = String(group.titleKey ?? group.title ?? '').trim();

          if (!titleKey) {
            return null;
          }

          return {
            id,
            titleKey,
            links,
          };
        })
        .filter(Boolean)
    : [];

  const groupIds = groups.map((group) => group.id).join(',');
  if (groupIds === 'about,docs,related-projects,friendly-links') {
    return DEFAULT_FOOTER_CONFIG;
  }

  return {
    version: 1,
    groups,
  };
}

export function parseFooterConfig(rawValue) {
  if (!rawValue || typeof rawValue !== 'string') {
    return null;
  }

  try {
    return normalizeFooterConfig(JSON.parse(rawValue));
  } catch {
    return null;
  }
}

export function stringifyFooterConfig(config) {
  return JSON.stringify(
    normalizeFooterConfig(config) ?? DEFAULT_FOOTER_CONFIG,
    null,
    2,
  );
}
