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

import React, { useRef, useEffect, useState } from 'react';
import { useTokenKeys } from '../../hooks/chat/useTokenKeys';
import { Spin } from '@douyinfe/semi-ui';
import { useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { takeEmbeddedInitialPrompt } from '../../app/utils/embedded';

const ChatPage = () => {
  const { t } = useTranslation();
  const { id } = useParams();
  const { keys, serverAddress, isLoading } = useTokenKeys(id);
  const initialPromptRef = useRef(takeEmbeddedInitialPrompt());
  const [clipboardHint, setClipboardHint] = useState('');

  const comLink = (key) => {
    // console.log('chatLink:', chatLink);
    if (!serverAddress || !key) return '';
    let link = '';
    if (id) {
      let chats = localStorage.getItem('chats');
      if (chats) {
        chats = JSON.parse(chats);
        if (Array.isArray(chats) && chats.length > 0) {
          for (let k in chats[id]) {
            link = chats[id][k];
            link = link.replaceAll(
              '{address}',
              encodeURIComponent(serverAddress),
            );
            link = link.replaceAll('{key}', 'sk-' + key);
            if (initialPromptRef.current) {
              link = link.replaceAll(
                '{prompt}',
                encodeURIComponent(initialPromptRef.current),
              );
            }
          }
        }
      }
    }
    return link;
  };

  const iframeSrc = keys.length > 0 ? comLink(keys[0]) : '';

  useEffect(() => {
    const prompt = initialPromptRef.current;
    if (!prompt) return;

    // 将内容写入剪贴板，用户进入聊天后直接 Ctrl+V 粘贴
    navigator.clipboard.writeText(prompt).then(() => {
      setClipboardHint(prompt);
      // 5 秒后自动隐藏提示
      setTimeout(() => setClipboardHint(''), 5000);
    }).catch(() => {
      // 剪贴板写入失败（如未获得权限）时不显示提示
    });
  }, []);

  const clipboardBanner = clipboardHint && (
    <div
      style={{
        position: 'fixed',
        top: '72px',
        left: '50%',
        transform: 'translateX(-50%)',
        zIndex: 9999,
        background: 'rgba(30,30,30,0.92)',
        color: '#fff',
        padding: '8px 20px',
        borderRadius: '8px',
        fontSize: '13px',
        maxWidth: '480px',
        whiteSpace: 'nowrap',
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        pointerEvents: 'none',
        boxShadow: '0 2px 12px rgba(0,0,0,0.2)',
      }}
    >
      📋 内容已复制，请在输入框中按 Ctrl+V 粘贴：{clipboardHint.length > 40 ? clipboardHint.slice(0, 40) + '…' : clipboardHint}
    </div>
  );

  return !isLoading && iframeSrc ? (
    <div style={{ position: 'relative', width: '100%', height: '100%' }}>
      {clipboardBanner}
      <iframe
        src={iframeSrc}
        style={{
          width: '100%',
          height: 'calc(100vh - 64px)',
          border: 'none',
          marginTop: '64px',
        }}
        title='Token Frame'
        allow='camera;microphone'
      />
    </div>
  ) : (
    <div className='fixed inset-0 w-screen h-screen flex items-center justify-center bg-white/80 z-[1000] mt-[60px]'>
      {clipboardBanner}
      <div className='flex flex-col items-center'>
        <Spin size='large' spinning={true} tip={null} />
        <span
          className='whitespace-nowrap mt-2 text-center'
          style={{ color: 'var(--semi-color-primary)' }}
        >
          {t('正在跳转...')}
        </span>
      </div>
    </div>
  );
};

export default ChatPage;
