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
import React, { useEffect, useMemo, useState } from 'react';
import {
  Banner,
  Button,
  Input,
  InputNumber,
  Radio,
  RadioGroup,
  Table,
  TextArea,
  Typography,
} from '@douyinfe/semi-ui';
import { IconCopy, IconDelete, IconPlus } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import { API, copy, showError, showSuccess } from '../../../helpers';

const { Text } = Typography;

const OPTION_KEY = 'thirdpartysd2_pricing.matrix';

const DEFAULT_MATRIX = {
  'dreamina-seedance-2-0-260128': {
    '480p': { no_video: 7.0, with_video: 4.3 },
    '720p': { no_video: 7.0, with_video: 4.3 },
    '1080p': { no_video: 7.7, with_video: 4.7 },
    '4k': { no_video: 4.0, with_video: 2.4 },
  },
  'dreamina-seedance-2-0-fast-260128': {
    '480p': { no_video: 5.6, with_video: 3.3 },
    '720p': { no_video: 5.6, with_video: 3.3 },
  },
};

function matrixToRows(matrix) {
  let nextId = 1;
  const rows = [];
  Object.entries(matrix || {}).forEach(([model, resolutions]) => {
    Object.entries(resolutions || {}).forEach(([resolution, pricing]) => {
      rows.push({
        id: nextId++,
        model,
        resolution,
        noVideo: Number(pricing?.no_video) || 0,
        withVideo: Number(pricing?.with_video) || 0,
      });
    });
  });
  return rows;
}

function rowsToMatrix(rows) {
  const matrix = {};
  for (const row of rows) {
    const model = row.model.trim();
    const resolution = row.resolution.trim();
    if (!model || !resolution) continue;
    if (!matrix[model]) {
      matrix[model] = {};
    }
    matrix[model][resolution] = {
      no_video: Number(row.noVideo) || 0,
      with_video: Number(row.withVideo) || 0,
    };
  }
  return matrix;
}

function findDuplicateModelResolution(rows) {
  const seen = new Set();
  for (const row of rows) {
    const model = row.model.trim();
    const resolution = row.resolution.trim();
    if (!model || !resolution) continue;
    const key = `${model}\u0000${resolution}`;
    if (seen.has(key)) {
      return { model, resolution };
    }
    seen.add(key);
  }
  return null;
}

export default function ThirdPartySD2PricingSettings({ options }) {
  const { t } = useTranslation();
  const [rows, setRows] = useState([]);
  const [mode, setMode] = useState('visual');
  const [jsonText, setJsonText] = useState('');
  const [jsonError, setJsonError] = useState('');
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let matrix = {};
    try {
      const raw = options?.[OPTION_KEY];
      if (raw) {
        matrix = typeof raw === 'string' ? JSON.parse(raw) : raw;
      }
    } catch {
      matrix = {};
    }

    if (!matrix || Object.keys(matrix).length === 0) {
      matrix = { ...DEFAULT_MATRIX };
    }

    setRows(matrixToRows(matrix));
    setJsonText(JSON.stringify(matrix, null, 2));
    setJsonError('');
  }, [options]);

  const syncToJson = (nextRows) => {
    setRows(nextRows);
    setJsonText(JSON.stringify(rowsToMatrix(nextRows), null, 2));
    setJsonError('');
  };

  const syncToVisual = (text) => {
    setJsonText(text);
    try {
      const parsed = JSON.parse(text);
      if (typeof parsed !== 'object' || Array.isArray(parsed) || parsed === null) {
        setJsonError(t('JSON 必须是模型 -> 分辨率 -> 价格对象'));
        return;
      }
      setRows(matrixToRows(parsed));
      setJsonError('');
    } catch (e) {
      setJsonError(e.message);
    }
  };

  const updateRow = (id, field, value) => {
    syncToJson(rows.map((row) => (row.id === id ? { ...row, [field]: value } : row)));
  };

  const addRow = () => {
    syncToJson([
      ...rows,
      {
        id: Date.now(),
        model: '',
        resolution: '720p',
        noVideo: 0,
        withVideo: 0,
      },
    ]);
  };

  const removeRow = (id) => {
    syncToJson(rows.filter((row) => row.id !== id));
  };

  const resetToDefault = () => {
    syncToJson(matrixToRows(DEFAULT_MATRIX));
  };

  const currentMatrix = useMemo(() => rowsToMatrix(rows), [rows]);

  const handleSave = async () => {
    const duplicate = findDuplicateModelResolution(rows);
    if (duplicate) {
      showError(
        t('已存在相同模型和分辨率配置，不能重复保存') +
          ` (${duplicate.model} / ${duplicate.resolution})`,
      );
      return;
    }
    setSaving(true);
    try {
      const res = await API.put('/api/option/', {
        key: OPTION_KEY,
        value: JSON.stringify(currentMatrix),
      });
      if (res.data.success) {
        showSuccess(t('保存成功'));
      } else {
        showError(res.data.message || t('保存失败'));
      }
    } catch (e) {
      showError(e.message);
    } finally {
      setSaving(false);
    }
  };

  const columns = [
    {
      title: t('模型'),
      dataIndex: 'model',
      render: (text, record) => (
        <Input
          value={text}
          placeholder='dreamina-seedance-2-0-260128'
          onChange={(val) => updateRow(record.id, 'model', val)}
          style={{ width: '100%' }}
        />
      ),
    },
    {
      title: t('分辨率'),
      dataIndex: 'resolution',
      width: 140,
      render: (text, record) => (
        <Input
          value={text}
          placeholder='720p'
          onChange={(val) => updateRow(record.id, 'resolution', val)}
          style={{ width: '100%' }}
        />
      ),
    },
    {
      title: `${t('无视频输入价格')} ($/1M tokens)`,
      dataIndex: 'noVideo',
      width: 180,
      render: (value, record) => (
        <InputNumber
          value={value}
          min={0}
          step={0.1}
          onChange={(v) => updateRow(record.id, 'noVideo', v ?? 0)}
          style={{ width: '100%' }}
        />
      ),
    },
    {
      title: `${t('带视频输入价格')} ($/1M tokens)`,
      dataIndex: 'withVideo',
      width: 180,
      render: (value, record) => (
        <InputNumber
          value={value}
          min={0}
          step={0.1}
          onChange={(v) => updateRow(record.id, 'withVideo', v ?? 0)}
          style={{ width: '100%' }}
        />
      ),
    },
    {
      title: t('操作'),
      width: 60,
      render: (_, record) => (
        <Button
          icon={<IconDelete />}
          type='danger'
          theme='borderless'
          size='small'
          onClick={() => removeRow(record.id)}
        />
      ),
    },
  ];

  return (
    <div style={{ maxWidth: 960 }}>
      <Banner
        type='info'
        description={
          <>
            <div>{t('按分辨率和是否带视频输入配置 thirdpartysd2 渠道的 token 单价（$/1M tokens）。')}</div>
            <div style={{ marginTop: 4 }}>
              <Text strong>{t('说明')}：</Text>
              {t('这些价格只覆盖 thirdpartysd2 任务渠道，不影响其他模型和渠道的通用倍率计费。')}
            </div>
          </>
        }
        style={{ marginBottom: 16 }}
      />

      <RadioGroup
        type='button'
        size='small'
        value={mode}
        onChange={(e) => setMode(e.target.value)}
        style={{ marginBottom: 12 }}
      >
        <Radio value='visual'>{t('可视化')}</Radio>
        <Radio value='json'>JSON</Radio>
      </RadioGroup>

      {mode === 'visual' ? (
        <>
          <Table
            dataSource={rows}
            columns={columns}
            pagination={false}
            size='small'
            rowKey='id'
          />
          <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
            <Button icon={<IconPlus />} onClick={addRow}>
              {t('添加')}
            </Button>
            <Button theme='borderless' onClick={resetToDefault}>
              {t('恢复默认')}
            </Button>
          </div>
        </>
      ) : (
        <>
          <TextArea
            value={jsonText}
            onChange={syncToVisual}
            autosize={{ minRows: 10, maxRows: 24 }}
            style={{ fontFamily: 'monospace', fontSize: 13 }}
          />
          {jsonError && (
            <Text type='danger' size='small' style={{ display: 'block', marginTop: 4 }}>
              {jsonError}
            </Text>
          )}
          <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
            <Button
              icon={<IconCopy />}
              size='small'
              theme='borderless'
              onClick={() => { copy(jsonText, t('JSON')); }}
            >
              {t('复制')}
            </Button>
            <Button size='small' theme='borderless' onClick={resetToDefault}>
              {t('恢复默认')}
            </Button>
          </div>
        </>
      )}

      <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 16 }}>
        <Button
          theme='solid'
          type='primary'
          loading={saving}
          disabled={mode === 'json' && !!jsonError}
          onClick={handleSave}
        >
          {t('保存')}
        </Button>
      </div>
    </div>
  );
}
