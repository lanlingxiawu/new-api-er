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

import React, { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Banner,
  Button,
  Form,
  InputNumber,
  Space,
  Spin,
  Switch,
  Typography,
} from '@douyinfe/semi-ui';
import { API, showError, showSuccess } from '../../../helpers';

const { Text } = Typography;

// CPU / trace 采样需要秒数；其余是即时快照。
const NEEDS_SECONDS = ['profile', 'trace'];
const MAX_SECONDS = 120;
// goroutine 文本变体的合成 key（?debug=2，输出可读全栈）
const GOROUTINE_TEXT_KEY = 'goroutine:text';

/**
 * profile 的可读名。按钮上显示它，原始名（pprof 的端点名）并排保留：
 * 用户查 go tool pprof 文档、对照 URL 时靠的都是原始名，丢了就对不上号。
 */
const PROFILE_LABELS = {
  profile: 'CPU 采样',
  heap: '堆内存',
  goroutine: '协程栈',
  allocs: '累计分配',
  block: '阻塞',
  mutex: '互斥锁竞争',
  threadcreate: '线程创建',
  trace: '执行追踪',
};

/**
 * pprof 开关 + profile 下载。开关即时生效（单个布尔值，没必要攒着一起保存），
 * 下载按钮读的是服务端真实状态而不是表单值。
 */
export default function SettingsProfiling() {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(true);
  const [enabled, setEnabled] = useState(false);
  const [profiles, setProfiles] = useState([]);
  const [seconds, setSeconds] = useState(30);
  const [switching, setSwitching] = useState(false);
  const [downloading, setDownloading] = useState('');

  const fetchStatus = useCallback(async () => {
    try {
      const res = await API.get('/api/system-info/pprof-status');
      if (res.data.success) {
        setEnabled(res.data.data.enabled === true);
        setProfiles(res.data.data.profiles || []);
      }
    } catch (error) {
      // 读状态失败不打扰用户，保留上一次结果
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchStatus();
  }, [fetchStatus]);

  async function toggle(checked) {
    setSwitching(true);
    try {
      const res = await API.put('/api/option/', {
        key: 'pprof_setting.enabled',
        value: String(checked),
      });
      if (res.data.success) {
        showSuccess(t('已生效'));
        await fetchStatus();
      } else {
        showError(res.data.message);
      }
    } catch (error) {
      showError(t('保存失败，请重试'));
    } finally {
      setSwitching(false);
    }
  }

  /**
   * 下载必须走 API 实例，不能用 window.open：后端鉴权要求 New-Api-User 请求头
   * （middleware/auth.go 的 authHelper），浏览器直接打开链接带不了自定义头，一律 401。
   * 业务错误是 HTTP 200 + JSON 体，按 Content-Type 判出来弹提示，
   * 否则用户会存下一个内容是错误信息的"profile 文件"。
   */
  // key 用 profile 名；goroutine 文本变体用 'goroutine:text' 区分，各按钮 loading 互不干扰。
  async function download(key) {
    const isText = key === GOROUTINE_TEXT_KEY;
    const name = isText ? 'goroutine' : key;
    const needsSeconds = NEEDS_SECONDS.includes(name);
    if (needsSeconds && (!seconds || seconds < 1 || seconds > MAX_SECONDS)) {
      showError(t('单次采样最长 {{max}} 秒', { max: MAX_SECONDS }));
      return;
    }

    setDownloading(key);
    try {
      // debug=2 让 goroutine 输出可读全栈文本（而非 go tool pprof 用的 protobuf）
      const params = new URLSearchParams();
      if (needsSeconds) params.set('seconds', seconds);
      if (isText) params.set('debug', 2);
      const query = params.toString() ? `?${params.toString()}` : '';
      const res = await API.get(`/api/system-info/pprof/${name}${query}`, {
        responseType: 'blob',
        timeout: 0, // CPU / trace 采样会挂住连接几十秒
      });

      const blob = res.data;
      if (blob.type && blob.type.includes('application/json')) {
        const text = await blob.text();
        let message = text;
        try {
          message = JSON.parse(text).message || text;
        } catch (error) {
          // 不是 JSON 就原样提示
        }
        throw new Error(message);
      }

      const stamp = new Date().toISOString().replace(/[-:T]/g, '').slice(0, 14);
      // debug 文本存 .txt；trace 存 .trace；其余是 protobuf，存 .pprof
      const extension = isText ? 'txt' : name === 'trace' ? 'trace' : 'pprof';
      const objectUrl = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = objectUrl;
      link.download = `${name}-${stamp}.${extension}`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(objectUrl);
    } catch (error) {
      showError(error.message || t('下载失败，请重试'));
    } finally {
      setDownloading('');
    }
  }

  return (
    <Spin spinning={loading}>
      <Form style={{ marginBottom: 15 }}>
        <Form.Section text={t('性能剖析 (pprof)')}>
          <Banner
            type='info'
            description={t(
              '下载 Go pprof profile 文件，用 go tool pprof 离线分析。开关热更新，无需重启。',
            )}
            style={{ marginBottom: 16 }}
          />

          <Space align='start' style={{ marginBottom: 16 }}>
            <Switch
              checked={enabled}
              loading={switching}
              size='default'
              checkedText='｜'
              uncheckedText='〇'
              onChange={toggle}
            />
            <div>
              <Text>{t('开启 pprof 下载')}</Text>
              <div>
                <Text size='small' type='tertiary'>
                  {t(
                    'heap dump 含内存中的上游密钥与用户令牌，非排障期间请保持关闭。',
                  )}
                </Text>
              </div>
            </div>
          </Space>

          <div>
            <Space wrap>
              <InputNumber
                value={seconds}
                min={1}
                max={MAX_SECONDS}
                disabled={!enabled}
                onChange={(value) => setSeconds(value)}
                style={{ width: 120 }}
              />
              {profiles.map((name) => (
                <React.Fragment key={name}>
                  <Button
                    size='small'
                    disabled={!enabled || Boolean(downloading)}
                    loading={downloading === name}
                    onClick={() => download(name)}
                  >
                    {PROFILE_LABELS[name] ? t(PROFILE_LABELS[name]) : name}
                    <Text
                      size='small'
                      type='tertiary'
                      style={{ marginLeft: 6, fontFamily: 'monospace' }}
                    >
                      {name}
                    </Text>
                  </Button>
                  {/* goroutine 额外给一个文本格式：?debug=2 输出可读全栈，
                      排查"某几个协程卡死"时比 protobuf 直观 */}
                  {name === 'goroutine' && (
                    <Button
                      size='small'
                      disabled={!enabled || Boolean(downloading)}
                      loading={downloading === GOROUTINE_TEXT_KEY}
                      onClick={() => download(GOROUTINE_TEXT_KEY)}
                    >
                      {t('协程栈文本')}
                      <Text
                        size='small'
                        type='tertiary'
                        style={{ marginLeft: 6, fontFamily: 'monospace' }}
                      >
                        debug=2
                      </Text>
                    </Button>
                  )}
                </React.Fragment>
              ))}
            </Space>
          </div>

          <div style={{ marginTop: 8 }}>
            <Text size='small' type='tertiary'>
              {t(
                '下载后在本地用 go tool pprof 分析；trace 文件用 go tool trace。',
              )}
            </Text>
          </div>
          <div style={{ marginTop: 4 }}>
            <Text size='small' type='tertiary'>
              {t(
                'cpu 与 trace 按上方秒数采样；block、mutex 未在启动时开启采样率时为空。',
              )}
            </Text>
          </div>
        </Form.Section>
      </Form>
    </Spin>
  );
}
