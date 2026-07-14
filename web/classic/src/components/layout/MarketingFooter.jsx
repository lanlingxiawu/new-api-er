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

import React, { useContext, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { StatusContext } from '../../context/Status';
import { getFooterHTML, parseFooterConfig } from '../../helpers';
import FigmaFooter from './FigmaFooter';
import { buildContactGroup } from './figmaHomeShared';

const MarketingFooter = () => {
  const { t } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const footerConfig = useMemo(() => {
    const statusFooter = statusState?.status?.footer_html;
    return (
      parseFooterConfig(statusFooter) || parseFooterConfig(getFooterHTML()) || null
    );
  }, [statusState?.status?.footer_html]);

  const contactGroup = useMemo(
    () => buildContactGroup(statusState?.status, t),
    [statusState?.status, t],
  );

  return <FigmaFooter footerConfig={footerConfig} contactGroup={contactGroup} />;
};

export default MarketingFooter;
