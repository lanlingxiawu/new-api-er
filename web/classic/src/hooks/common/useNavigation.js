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

import { useMemo } from 'react';

const defaultModules = {
  home: true,
  console: true,
  pricing: {
    enabled: true,
    requireAuth: false,
  },
  docs: true,
  about: true,
};

const isModuleEnabled = (modules, key) => {
  const value = modules?.[key];
  if (value && typeof value === 'object') {
    return value.enabled !== false;
  }
  if (value === undefined) {
    const fallback = defaultModules[key];
    return typeof fallback === 'object' ? fallback.enabled !== false : fallback;
  }
  return value === true;
};

export const useNavigation = (t, docsLink, headerNavModules) => {
  const mainNavLinks = useMemo(() => {
    const modules = headerNavModules || defaultModules;

    const allLinks = [
      {
        text: t('首页'),
        itemKey: 'home',
        to: '/',
      },
      {
        text: t('控制台'),
        itemKey: 'console',
        to: '/console',
      },
      {
        text: t('模型广场'),
        itemKey: 'pricing',
        to: '/pricing',
      },
      ...(docsLink
        ? [
            {
              text: t('文档'),
              itemKey: 'docs',
              isExternal: true,
              externalLink: docsLink,
            },
          ]
        : []),
      {
        text: t('关于'),
        itemKey: 'about',
        to: '/about',
      },
    ];

    return allLinks.filter((link) => {
      if (link.itemKey === 'docs') {
        return docsLink && isModuleEnabled(modules, 'docs');
      }
      return isModuleEnabled(modules, link.itemKey);
    });
  }, [t, docsLink, headerNavModules]);

  return {
    mainNavLinks,
  };
};
