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
import {
  DEFAULT_FOOTER_CONFIG,
  getFooterHTML,
  getSystemName,
  parseFooterConfig,
} from '../../helpers';
import { StatusContext } from '../../context/Status';
import FigmaFooter from './FigmaFooter';

const FooterBar = () => {
  const [statusState] = useContext(StatusContext);
  const systemName = getSystemName();
  const currentYear = new Date().getFullYear();
  const footerConfig = useMemo(() => {
    const statusFooter = statusState?.status?.footer_html;
    return (
      parseFooterConfig(statusFooter) ||
      parseFooterConfig(getFooterHTML()) ||
      DEFAULT_FOOTER_CONFIG
    );
  }, [statusState?.status?.footer_html]);

  return (
    <FigmaFooter
      footerConfig={footerConfig}
      copyrightText={`Copyright ${currentYear} ${systemName}. All rights reserved.`}
    />
  );
};

export default FooterBar;
