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

import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Select, Spin } from '@douyinfe/semi-ui';
import { API, showError, showSuccess } from '../../../../helpers';

const ellipsisStyle = {
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
};

const buildOption = (emp) => {
  const name =
    (emp.display_name || '').trim() ||
    (emp.username || '').trim() ||
    `#${emp.user_id}`;
  const primary = `${name} (ID: ${emp.user_id})`;
  const remark = (emp.remark || '').trim();
  return {
    value: emp.id,
    name: primary,
    remark,
    // 下拉项：两行展示（名称 + 备注）。
    label: (
      <div style={{ display: 'flex', flexDirection: 'column', lineHeight: 1.3 }}>
        <span style={ellipsisStyle}>{primary}</span>
        {remark ? (
          <span
            style={{
              ...ellipsisStyle,
              fontSize: 12,
              color: 'var(--semi-color-text-2)',
            }}
          >
            {remark}
          </span>
        ) : null}
      </div>
    ),
    employee: emp,
  };
};

/**
 * 分配员工下拉框：把当前用户（客户）分配给某个归属员工。
 * - 可搜索（远程），支持按用户名/昵称/邮箱/备注搜索；下拉项显示员工备注。
 * - 选择后立即调用分配接口生效；清空即取消分配。
 * - value 为 employee_profiles.id（分配/取消分配接口以此为路径参数）。
 */
const EmployeeAssignSelect = ({ userId, inviterId, onChanged }) => {
  const { t } = useTranslation();
  const [options, setOptions] = useState([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [value, setValue] = useState(undefined); // employee.id
  const [selectedOption, setSelectedOption] = useState(null);
  const debounceRef = useRef(null);

  const fetchEmployees = async (keyword = '') => {
    setLoading(true);
    try {
      const res = await API.get('/api/admin/employee', {
        params: { keyword, status: 1, page: 1, page_size: 20 },
      });
      const items = res.data?.data?.items || [];
      setOptions(items.map(buildOption));
    } catch (e) {
      showError(e.message);
    } finally {
      setLoading(false);
    }
  };

  // 载入当前归属员工（用 inviter_id 反查，保证分页外也能正确回显）。
  useEffect(() => {
    let cancelled = false;
    const loadCurrent = async () => {
      if (inviterId && inviterId > 0) {
        try {
          const res = await API.get('/api/admin/employee', {
            params: { user_id: inviterId, page: 1, page_size: 1 },
          });
          const emp = res.data?.data?.items?.[0];
          if (!cancelled && emp) {
            setValue(emp.id);
            setSelectedOption(buildOption(emp));
          }
        } catch (e) {
          /* ignore */
        }
      } else if (!cancelled) {
        setValue(undefined);
        setSelectedOption(null);
      }
    };
    loadCurrent();
    fetchEmployees('');
    return () => {
      cancelled = true;
    };
  }, [userId, inviterId]);

  // 始终把已选员工并入选项列表，避免其不在当前搜索分页内时标签丢失。
  const optionList = useMemo(() => {
    if (
      selectedOption &&
      !options.some((o) => o.value === selectedOption.value)
    ) {
      return [selectedOption, ...options];
    }
    return options;
  }, [options, selectedOption]);

  const handleSearch = (keyword) => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(
      () => fetchEmployees((keyword || '').trim()),
      300,
    );
  };

  const handleAssign = async (nextValue) => {
    const opt = options.find((o) => o.value === nextValue) || selectedOption;
    setSaving(true);
    try {
      const res = await API.post(
        `/api/admin/employee/${nextValue}/assign-customer`,
        { user_id: parseInt(userId) },
      );
      if (res.data.success) {
        setValue(nextValue);
        if (opt) setSelectedOption(opt);
        showSuccess(t('分配员工成功'));
        onChanged && onChanged();
      } else {
        showError(res.data.message);
      }
    } catch (e) {
      showError(e.message);
    } finally {
      setSaving(false);
    }
  };

  const handleUnassign = async () => {
    if (!value) {
      setValue(undefined);
      setSelectedOption(null);
      return;
    }
    const current = value;
    setSaving(true);
    try {
      const res = await API.delete(
        `/api/admin/employee/${current}/customer/${userId}`,
      );
      if (res.data.success) {
        setValue(undefined);
        setSelectedOption(null);
        showSuccess(t('取消分配员工成功'));
        onChanged && onChanged();
      } else {
        showError(res.data.message);
      }
    } catch (e) {
      showError(e.message);
    } finally {
      setSaving(false);
    }
  };

  const handleChange = (nextValue) => {
    if (nextValue === undefined || nextValue === null || nextValue === '') {
      handleUnassign();
      return;
    }
    if (nextValue === value) return;
    handleAssign(nextValue);
  };

  // 选中态收敛为单行（名称 · 备注），避免选择框被两行标签撑高。
  const renderSelectedItem = (optionNode) => {
    if (!optionNode) return '';
    const remark = optionNode.remark;
    return remark ? `${optionNode.name} · ${remark}` : optionNode.name;
  };

  return (
    <Select
      style={{ width: '100%' }}
      placeholder={t('未分配')}
      filter
      remote
      loading={loading}
      onSearch={handleSearch}
      optionList={optionList}
      value={value}
      onChange={handleChange}
      renderSelectedItem={renderSelectedItem}
      showClear
      disabled={saving}
      emptyContent={loading ? <Spin /> : t('无匹配员工')}
    />
  );
};

export default EmployeeAssignSelect;
