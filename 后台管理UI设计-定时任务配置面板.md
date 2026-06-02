# 后台管理UI设计 — 定时任务配置可视化面板

**版本**: 1.0  
**日期**: 2026-06-02  
**适用**: React 19 + TypeScript + Rsbuild + Base UI

---

## 1. 功能概览

### 1.1 管理面板的主要功能

```
后台管理系统
├─ 定时任务管理
│  ├─ 任务列表查看
│  ├─ 参数实时编辑
│  ├─ 任务启用/禁用
│  ├─ 手动执行任务
│  └─ 执行历史查看
│
├─ 集群监控
│  ├─ 节点列表
│  ├─ 分布式锁状态
│  ├─ Master选举情况
│  └─ 节点健康度
│
├─ 执行日志
│  ├─ 任务执行时间线
│  ├─ 错误日志详情
│  ├─ 性能指标展示
│  └─ 日志搜索和筛选
│
└─ 告警和通知
   ├─ 任务失败告警
   ├─ 锁超时告警
   └─ 告警历史
```

### 1.2 用户权限

```
角色: Super Admin (最高权限)
├─ 查看所有配置
├─ 修改任务参数
├─ 启用/禁用任务
├─ 手动执行任务
├─ 查看集群状态
└─ 查看详细日志

角色: DevOps (运维权限)
├─ 查看配置 ✓
├─ 修改参数（某些字段） ✓
├─ 查看集群状态 ✓
└─ 查看日志 ✓

角色: Developer (查看权限)
├─ 查看配置 ✓
├─ 查看执行状态 ✓
└─ 查看日志 ✓
```

---

## 2. UI界面设计

### 2.1 定时任务管理主页

```
┌─────────────────────────────────────────────────────────────────┐
│ 定时任务管理系统                         [帮助] [设置] [用户菜单]  │
├─────────────────────────────────────────────────────────────────┤
│                                                                   │
│ 【左侧导航】              【主内容区域】                           │
│ ┌──────────────┐  ┌───────────────────────────────────────────┐ │
│ │ 📊 仪表盘     │  │ 定时任务配置面板                          │ │
│ │ ⚙️  任务管理   │  │                                           │ │
│ │ 📡 集群监控   │  │ 快速统计:                                 │ │
│ │ 📋 执行日志   │  │ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐     │ │
│ │ 🔔 告警中心   │  │ │ 6个  │ │ 5个  │ │ 1个  │ │ 3个  │     │ │
│ │ ⚡ 系统设置   │  │ │任务  │ │启用  │ │运行  │ │失败  │     │ │
│ │ 🚪 退出登录   │  │ └──────┘ └──────┘ └──────┘ └──────┘     │ │
│ └──────────────┘  │                                           │ │
│                   │ [按状态筛选] [按需求日期] [导出设置]        │ │
│                   │                                           │ │
│                   │ 任务列表:                                  │ │
│                   │ ┌─────────────────────────────────────┐   │ │
│                   │ │ 任务名        状态    间隔  超时  操作  │   │ │
│                   │ ├─────────────────────────────────────┤   │ │
│                   │ │ GroupStatus.. ✓运行中  3600  300 [编辑] │   │ │
│                   │ │ AlertsGener.. ✓运行中   300  120 [编辑] │   │ │
│                   │ │ ChannelAuto.. ✓运行中  1800  600 [编辑] │   │ │
│                   │ │ ...                              │   │ │
│                   │ └─────────────────────────────────────┘   │ │
│                   │                                           │ │
│                   │ [首页] [上一页] 1/2 [下一页] [末页]        │ │
│ └───────────────────────────────────────────────────────────────┘ │
│                                                                   │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 任务详情编辑页面

```
┌─────────────────────────────────────────────────────────────────┐
│ 任务详情 - GroupStatusAggregation                    [返回] [关闭] │
├─────────────────────────────────────────────────────────────────┤
│                                                                   │
│ 基本信息                                                         │ │
│ ┌───────────────────────────────────────────────────────────┐   │ │
│ │ 任务名称 (不可编辑)                                        │   │ │
│ │ GroupStatusAggregation                                   │   │ │
│ │                                                         │   │ │
│ │ 任务描述:                                               │   │ │
│ │ 每小时聚合一次分组状态数据                              │   │ │
│ │                                                         │   │ │
│ │ 创建时间: 2026-06-01 10:00:00                          │   │ │
│ │ 最后更新: 2026-06-02 14:30:00  (更新者: admin)         │   │ │
│ └───────────────────────────────────────────────────────────┘   │ │
│                                                                   │ │
│ 执行配置                                                         │ │
│ ┌───────────────────────────────────────────────────────────┐   │ │
│ │                                                         │   │ │
│ │ 启用此任务   [✓ 开]                                     │   │ │
│ │ 💡 禁用后任务将不会执行，已有的配置会保留               │   │ │
│ │                                                         │   │ │
│ │ 执行间隔 (秒)                                          │   │ │
│ │ ┌─────────────┐  当前值: 3600s (1小时)                │   │ │
│ │ │   3600  ⬆️⬇️  │                                         │   │ │
│ │ └─────────────┘  推荐值: 3600-7200                     │   │ │
│ │                  💡 修改后立即生效，无需重启应用        │   │ │
│ │                                                         │   │ │
│ │ 任务超时 (秒)                                          │   │ │
│ │ ┌─────────────┐  当前值: 300s (5分钟)                 │   │ │
│ │ │   300   ⬆️⬇️  │                                         │   │ │
│ │ └─────────────┘  推荐值: 300-600                       │   │ │
│ │                  💡 任务执行时间超过此值将被中断        │   │ │
│ │                                                         │   │ │
│ │ 仅在Master执行   [✓ 是]  (不可编辑)                    │   │ │
│ │ 💡 这是Master任务，仅在主节点执行，防止重复            │   │ │
│ │                                                         │   │ │
│ └───────────────────────────────────────────────────────────┘   │ │
│                                                                   │ │
│ 执行状态                                                         │ │
│ ┌───────────────────────────────────────────────────────────┐   │ │
│ │                                                         │   │ │
│ │ 上次执行    ✓ 成功   2026-06-02 13:23:45               │   │ │
│ │ 执行耗时    245 ms                                      │   │ │
│ │ 下次执行    2026-06-02 14:23:45   (还需等待: 8分钟)     │   │ │
│ │                                                         │   │ │
│ │ 执行历史：                                              │   │ │
│ │ ┌─────────────────────────────────────────────────────┐ │   │ │
│ │ │ 时间                 状态    耗时   节点              │ │   │ │
│ │ ├─────────────────────────────────────────────────────┤ │   │ │
│ │ │ 2026-06-02 13:23:45 ✓成功  245ms  prod-node-01    │ │   │ │
│ │ │ 2026-06-02 12:23:50 ✓成功  238ms  prod-node-01    │ │   │ │
│ │ │ 2026-06-02 11:24:02 ✓成功  251ms  prod-node-02    │ │   │ │
│ │ │ 2026-06-02 10:23:48 ❌失败  5120ms prod-node-01    │ │   │ │
│ │ │            错误: 数据库连接超时                       │ │   │ │
│ │ └─────────────────────────────────────────────────────┘ │   │ │
│ │                         [查看更多历史] [导出日志]        │   │ │
│ │                                                         │   │ │
│ └───────────────────────────────────────────────────────────┘   │ │
│                                                                   │ │
│ 操作                                                             │ │
│ ┌───────────────────────────────────────────────────────────┐   │ │
│ │                                                         │   │ │
│ │ [💾 保存修改]  [🔄 立即执行]  [📋 复制配置]  [🗑️ 重置]    │   │ │
│ │                                                         │   │ │
│ │ 💡 修改后点击"保存"按钮，配置会在1分钟内对所有节点生效   │   │ │
│ │                                                         │   │ │
│ └───────────────────────────────────────────────────────────┘   │ │
│                                                                   │
└─────────────────────────────────────────────────────────────────┘
```

### 2.3 集群监控页面

```
┌─────────────────────────────────────────────────────────────────┐
│ 集群监控                                            [刷新] [设置] │
├─────────────────────────────────────────────────────────────────┤
│                                                                   │
│ 集群概览                                                         │ │
│ ┌───────────────────────────────────────────────────────────┐   │ │
│ │                                                         │   │ │
│ │ 节点总数: 3        连接节点: 3        故障节点: 0      │   │ │
│ │ Master节点: prod-node-01 (IP: 10.0.0.1)                │   │ │
│ │ 上次同步: 2026-06-02 14:30:00 (6秒前)                  │   │ │
│ │                                                         │ │ │
│ └───────────────────────────────────────────────────────────┘   │ │
│                                                                   │ │
│ 节点列表                                                         │ │
│ ┌───────────────────────────────────────────────────────────┐   │ │
│ │ 节点ID              状态      IP        上次心跳  持有锁  │   │ │
│ ├───────────────────────────────────────────────────────────┤   │ │
│ │ prod-node-01      🟢 在线   10.0.0.1  2秒前    3个    │   │ │
│ │  ├─ GroupStatusAggregation (过期: 45分钟)                │   │ │
│ │  ├─ AlertsGeneration (过期: 3分钟)                      │   │ │
│ │  └─ ChannelUpstreamUpdate (过期: 12分钟)                │   │ │
│ │                                                         │   │ │
│ │ prod-node-02      🟢 在线   10.0.0.2  1秒前    0个    │   │ │
│ │                                                         │   │ │
│ │ prod-node-03      🟢 在线   10.0.0.3  4秒前    0个    │   │ │
│ │                                                         │   │ │
│ └───────────────────────────────────────────────────────────┘   │ │
│                                                                   │ │
│ 分布式锁状态                                                     │ │
│ ┌───────────────────────────────────────────────────────────┐   │ │
│ │                                                         │   │ │
│ │ 活跃的锁: 3 / 6 任务                                    │   │ │
│ │                                                         │   │ │
│ │ GroupStatusAggregation                                 │   │ │
│ │  锁持有者: prod-node-01      获取时间: 13:23:45        │   │ │
│ │  过期时间: 14:23:45 (还剩: 43分钟)                     │   │ │
│ │  🟢 锁状态正常                                          │   │ │
│ │                                                         │   │ │
│ │ AlertsGeneration                                       │   │ │
│ │  锁持有者: prod-node-01      获取时间: 14:28:45        │   │ │
│ │  过期时间: 14:33:45 (还剩: 3分钟)  ⚠️ 即将过期       │   │ │
│ │                                                         │   │ │
│ │ ChannelUpstreamUpdate                                  │   │ │
│ │  锁持有者: prod-node-01      获取时间: 14:18:30        │   │ │
│ │  过期时间: 14:38:30 (还剩: 12分钟)                     │   │ │
│ │  🟢 锁状态正常                                          │   │ │
│ │                                                         │   │ │
│ │ ChannelAutoTest                                        │   │ │
│ │  无活跃的锁  (所有节点可执行)                          │   │ │
│ │                                                         │   │ │
│ └───────────────────────────────────────────────────────────┘   │ │
│                                                                   │ │
│ 节点性能指标 (最近1小时)                                         │ │
│ ┌───────────────────────────────────────────────────────────┐   │ │
│ │                                                         │   │ │
│ │ prod-node-01 - CPU使用: 12% | 内存: 25% | 磁盘: 45%   │   │ │
│ │ [████░░░░░] [█████░░░░░░] [██████░░░░░░]              │   │ │
│ │                                                         │   │ │
│ │ prod-node-02 - CPU使用: 8%  | 内存: 18% | 磁盘: 42%   │   │ │
│ │ [████░░░░░] [█████░░░░░░] [██████░░░░░░]              │   │ │
│ │                                                         │   │ │
│ │ prod-node-03 - CPU使用: 10% | 内存: 22% | 磁盘: 43%   │   │ │
│ │ [████░░░░░] [█████░░░░░░] [██████░░░░░░]              │   │ │
│ │                                                         │   │ │
│ └───────────────────────────────────────────────────────────┘   │ │
│                                                                   │
└─────────────────────────────────────────────────────────────────┘
```

### 2.4 执行日志页面

```
┌─────────────────────────────────────────────────────────────────┐
│ 执行日志                  [刷新] [筛选] [导出] [清空]              │
├─────────────────────────────────────────────────────────────────┤
│                                                                   │
│ 筛选条件:                                                        │ │
│ 任务: [GroupStatusAggregation ▼] 状态: [全部 ▼] 日期: [今天 ▼]   │ │
│ 节点: [全部 ▼]                            [搜索] [应用筛选]       │ │
│                                                                   │ │
│ 日志列表:                                                        │ │
│ ┌───────────────────────────────────────────────────────────┐   │ │
│ │时间                任务                状态  耗时 节点     │   │ │
│ ├───────────────────────────────────────────────────────────┤   │ │
│ │2026-06-02 14:28:30 AlertsGeneration ✓成功 98ms  node-01 │   │ │
│ │                                      [详情▼]             │   │ │
│ │  └─ 检查分组健康度 4个                                   │   │ │
│ │     检查模型可用性 8个                                    │   │ │
│ │     生成告警 2个                                         │   │ │
│ │                                      [展开完整日志]        │   │ │
│ │                                                         │   │ │
│ │2026-06-02 13:23:45 GroupStatusAggr.. ✓成功 245ms node-01 │   │ │
│ │                                      [详情▼]             │   │ │
│ │                                                         │   │ │
│ │2026-06-02 10:23:48 GroupStatusAggr.. ❌失败 5120ms node-01 │   │ │
│ │                                      [详情▼]             │   │ │
│ │  └─ 错误详情:                                           │   │ │
│ │     类型: DatabaseError                                 │   │ │
│ │     消息: Connection timeout after 5000ms               │   │ │
│ │     堆栈: model.CalculateGroupMetrics:123               │   │ │
│ │          service.AggregateHourly:456                    │   │ │
│ │          ...                                            │   │ │
│ │                                      [下载错误报告]        │   │ │
│ │                                                         │   │ │
│ │2026-06-01 23:15:30 ChannelAutoTest  ⏱️ 超时 600000ms node-02 │   │ │
│ │                                      [详情▼]             │   │ │
│ │                                                         │   │ │
│ └───────────────────────────────────────────────────────────┘   │ │
│                                                                   │ │
│ [首页] [上一页] 1/15 [下一页] [末页]  显示: [20条 ▼] 条/页       │ │
│                                                                   │
└─────────────────────────────────────────────────────────────────┘
```

---

## 3. 前端组件实现

### 3.1 项目结构

```
web/default/src/
├─ routes/
│  └─ _authenticated/
│     └─ admin/
│        ├─ scheduler/
│        │  ├─ index.tsx              (主入口)
│        │  ├─ task-list.tsx          (任务列表)
│        │  ├─ task-editor.tsx        (任务编辑)
│        │  ├─ task-history.tsx       (执行历史)
│        │  └─ layout.tsx             (布局)
│        │
│        ├─ cluster/
│        │  ├─ index.tsx              (集群监控)
│        │  ├─ node-list.tsx          (节点列表)
│        │  ├─ lock-status.tsx        (锁状态)
│        │  └─ cluster-stats.tsx      (集群统计)
│        │
│        ├─ logs/
│        │  ├─ index.tsx              (日志查询)
│        │  ├─ log-viewer.tsx         (日志展示)
│        │  └─ log-filter.tsx         (日志筛选)
│        │
│        └─ alerts/
│           ├─ index.tsx              (告警中心)
│           └─ alert-list.tsx         (告警列表)
│
├─ features/
│  ├─ scheduler/
│  │  ├─ components/
│  │  │  ├─ TaskListTable.tsx
│  │  │  ├─ TaskEditForm.tsx
│  │  │  ├─ TaskStatusBadge.tsx
│  │  │  ├─ ExecutionHistoryTable.tsx
│  │  │  ├─ ClusterNodeList.tsx
│  │  │  ├─ DistributedLockViewer.tsx
│  │  │  └─ ...
│  │  │
│  │  ├─ hooks/
│  │  │  ├─ useSchedulerConfig.ts
│  │  │  ├─ useTaskExecution.ts
│  │  │  ├─ useClusterStatus.ts
│  │  │  └─ useExecutionLogs.ts
│  │  │
│  │  └─ types/
│  │     ├─ scheduler.ts
│  │     ├─ cluster.ts
│  │     └─ logs.ts
│  │
│  └─ api/
│     ├─ scheduler.ts         (API客户端)
│     └─ types.ts             (类型定义)
```

### 3.2 核心组件代码框架

**TaskListTable.tsx** - 任务列表表格

```typescript
import React, { useState, useEffect } from 'react';
import {
  Table, TableHead, TableBody, TableRow, TableCell,
  IconButton, Chip, Dialog, Button
} from '@mui/base';
import { useSchedulerConfig } from '../hooks/useSchedulerConfig';

export const TaskListTable: React.FC = () => {
  const { tasks, loading, error, updateTask } = useSchedulerConfig();
  const [editingTask, setEditingTask] = useState<string | null>(null);
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null);

  const getStatusColor = (status: string) => {
    switch (status) {
      case 'running': return 'success';
      case 'stopped': return 'warning';
      case 'failed': return 'danger';
      default: return 'default';
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex justify-between items-center">
        <h2 className="text-xl font-bold">定时任务列表</h2>
        <Button
          onClick={() => {/* 刷新列表 */}}
          className="ml-2"
        >
          🔄 刷新
        </Button>
      </div>

      {loading && <div>加载中...</div>}
      {error && <div className="text-red-500">{error}</div>}

      <Table className="w-full border-collapse border border-gray-200">
        <TableHead>
          <TableRow className="bg-gray-100">
            <TableCell className="border p-3">任务名称</TableCell>
            <TableCell className="border p-3">状态</TableCell>
            <TableCell className="border p-3">执行间隔</TableCell>
            <TableCell className="border p-3">超时时间</TableCell>
            <TableCell className="border p-3">上次执行</TableCell>
            <TableCell className="border p-3">下次执行</TableCell>
            <TableCell className="border p-3">操作</TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {tasks.map((task) => (
            <TableRow key={task.taskName} className="hover:bg-gray-50">
              <TableCell className="border p-3">{task.taskName}</TableCell>
              <TableCell className="border p-3">
                <Chip
                  label={task.enabled ? '启用中' : '已禁用'}
                  color={task.enabled ? 'success' : 'warning'}
                  size="sm"
                />
              </TableCell>
              <TableCell className="border p-3">
                {task.intervalSeconds}s ({Math.round(task.intervalSeconds / 60)}分)
              </TableCell>
              <TableCell className="border p-3">
                {task.timeoutSeconds}s
              </TableCell>
              <TableCell className="border p-3 text-sm">
                {new Date(task.lastRunTime * 1000).toLocaleString()}
                <br />
                <span className={task.lastRunStatus === 'success' ? 'text-green-600' : 'text-red-600'}>
                  {task.lastRunStatus}
                </span>
              </TableCell>
              <TableCell className="border p-3 text-sm">
                {new Date(task.nextRunTime * 1000).toLocaleString()}
              </TableCell>
              <TableCell className="border p-3 space-x-2">
                <IconButton
                  onClick={() => setEditingTask(task.taskName)}
                  title="编辑"
                  className="text-blue-600 hover:bg-blue-50"
                >
                  ✏️
                </IconButton>
                <IconButton
                  onClick={() => {
                    // 立即执行任务
                    updateTask(task.taskName, { executeNow: true });
                  }}
                  title="执行"
                  className="text-green-600 hover:bg-green-50"
                >
                  ▶️
                </IconButton>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      {/* 编辑对话框 */}
      {editingTask && (
        <TaskEditDialog
          taskName={editingTask}
          onClose={() => setEditingTask(null)}
          onSave={(config) => {
            updateTask(editingTask, config);
            setEditingTask(null);
          }}
        />
      )}
    </div>
  );
};
```

**TaskEditForm.tsx** - 任务编辑表单

```typescript
import React, { useState, useEffect } from 'react';
import { Button, FormControlLabel, TextField, Checkbox, Alert } from '@mui/base';
import { useSchedulerConfig } from '../hooks/useSchedulerConfig';

interface TaskEditFormProps {
  taskName: string;
  onSave: (config: any) => Promise<void>;
  onCancel: () => void;
}

export const TaskEditForm: React.FC<TaskEditFormProps> = ({
  taskName,
  onSave,
  onCancel,
}) => {
  const { getTaskConfig } = useSchedulerConfig();
  const [config, setConfig] = useState<any>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [history, setHistory] = useState<any[]>([]);

  useEffect(() => {
    const loadConfig = async () => {
      try {
        const data = await getTaskConfig(taskName);
        setConfig(data);
        setLoading(false);
      } catch (err) {
        setError('加载配置失败');
        setLoading(false);
      }
    };
    loadConfig();
  }, [taskName]);

  const handleSave = async () => {
    setSaving(true);
    try {
      await onSave(config);
      setError(null);
    } catch (err) {
      setError('保存失败：' + (err as any).message);
    } finally {
      setSaving(false);
    }
  };

  if (loading) return <div>加载中...</div>;
  if (!config) return <div>配置加载失败</div>;

  return (
    <div className="space-y-6">
      {error && <Alert severity="error">{error}</Alert>}

      {/* 基本信息 */}
      <div className="border rounded p-4">
        <h3 className="text-lg font-bold mb-4">基本信息</h3>
        <div className="space-y-3">
          <div>
            <label className="block text-sm font-medium">任务名称</label>
            <input
              type="text"
              value={config.taskName}
              disabled
              className="w-full p-2 border rounded bg-gray-100 cursor-not-allowed"
            />
          </div>
          <div>
            <label className="block text-sm font-medium">任务描述</label>
            <textarea
              value={config.description || ''}
              disabled
              rows={2}
              className="w-full p-2 border rounded bg-gray-50"
            />
          </div>
          <div className="grid grid-cols-2 gap-4 text-sm">
            <div>创建时间: {new Date(config.createdAt * 1000).toLocaleString()}</div>
            <div>更新时间: {new Date(config.updatedAt * 1000).toLocaleString()}</div>
          </div>
        </div>
      </div>

      {/* 执行配置 */}
      <div className="border rounded p-4">
        <h3 className="text-lg font-bold mb-4">执行配置</h3>
        <div className="space-y-4">
          {/* 启用开关 */}
          <FormControlLabel
            control={
              <Checkbox
                checked={config.enabled === 1}
                onChange={(e) =>
                  setConfig({ ...config, enabled: e.target.checked ? 1 : 0 })
                }
              />
            }
            label={`启用此任务 (当前: ${config.enabled === 1 ? '启用' : '禁用'})`}
          />

          {/* 执行间隔 */}
          <div>
            <label className="block text-sm font-medium mb-2">
              执行间隔 (秒)
            </label>
            <div className="flex items-center gap-3">
              <TextField
                type="number"
                value={config.intervalSeconds}
                onChange={(e) =>
                  setConfig({ ...config, intervalSeconds: parseInt(e.target.value) })
                }
                className="w-24"
              />
              <span className="text-sm text-gray-500">
                当前: {Math.round(config.intervalSeconds / 60)}分 {config.intervalSeconds % 60}秒
              </span>
              <button
                className="ml-2 px-3 py-1 bg-gray-200 rounded hover:bg-gray-300"
                onClick={() => {/* 显示推荐值 */}}
              >
                💡 推荐值
              </button>
            </div>
            <p className="text-xs text-gray-500 mt-1">
              修改后立即生效，无需重启应用
            </p>
          </div>

          {/* 超时时间 */}
          <div>
            <label className="block text-sm font-medium mb-2">
              任务超时 (秒)
            </label>
            <div className="flex items-center gap-3">
              <TextField
                type="number"
                value={config.timeoutSeconds}
                onChange={(e) =>
                  setConfig({ ...config, timeoutSeconds: parseInt(e.target.value) })
                }
                className="w-24"
              />
              <span className="text-sm text-gray-500">
                当前: {Math.round(config.timeoutSeconds / 60)}分
              </span>
            </div>
            <p className="text-xs text-gray-500 mt-1">
              任务执行时间超过此值将被中断
            </p>
          </div>

          {/* Master任务标记 */}
          <div className="bg-blue-50 p-3 rounded border border-blue-200">
            <div className="flex items-center gap-2">
              <span className="text-sm font-medium">仅在Master执行:</span>
              <span className="font-bold text-blue-600">
                {config.requiresMaster === 1 ? '是' : '否'}
              </span>
            </div>
            {config.requiresMaster === 1 && (
              <p className="text-xs text-gray-600 mt-2">
                💡 这是Master任务，仅在主节点执行，防止重复
              </p>
            )}
          </div>
        </div>
      </div>

      {/* 执行状态 */}
      <div className="border rounded p-4">
        <h3 className="text-lg font-bold mb-4">执行状态</h3>
        <div className="space-y-3 text-sm">
          <div className="flex justify-between">
            <span>上次执行:</span>
            <div className="text-right">
              {config.lastRunStatus === 'success' ? '✓ 成功' : '❌ 失败'}
              <br />
              {new Date(config.lastRunTime * 1000).toLocaleString()}
            </div>
          </div>
          <div className="flex justify-between">
            <span>下次执行:</span>
            <div className="text-right">
              {new Date(config.nextRunTime * 1000).toLocaleString()}
              <br />
              <span className="text-gray-500">
                还需等待: {Math.round((config.nextRunTime - Date.now() / 1000) / 60)}分钟
              </span>
            </div>
          </div>
          {config.lastError && (
            <div className="bg-red-50 p-2 rounded border border-red-200">
              <div className="font-medium text-red-800">错误详情</div>
              <div className="text-xs text-red-700 mt-1">{config.lastError}</div>
            </div>
          )}
        </div>
      </div>

      {/* 执行历史 */}
      <div className="border rounded p-4">
        <h3 className="text-lg font-bold mb-4">最近执行历史</h3>
        <div className="space-y-2 text-sm max-h-48 overflow-y-auto">
          {history.length === 0 ? (
            <div className="text-gray-500">暂无执行记录</div>
          ) : (
            history.map((h, i) => (
              <div key={i} className="flex justify-between items-center border-b pb-2">
                <div>
                  <div>{new Date(h.timestamp * 1000).toLocaleString()}</div>
                  <div className="text-xs text-gray-500">节点: {h.nodeId}</div>
                </div>
                <div className="text-right">
                  <div className={h.status === 'success' ? 'text-green-600' : 'text-red-600'}>
                    {h.status === 'success' ? '✓' : '❌'} {h.duration}ms
                  </div>
                </div>
              </div>
            ))
          )}
        </div>
      </div>

      {/* 操作按钮 */}
      <div className="flex gap-2 justify-end">
        <Button
          onClick={onCancel}
          className="px-4 py-2 border border-gray-300 rounded hover:bg-gray-50"
        >
          取消
        </Button>
        <Button
          onClick={() => {/* 立即执行 */}}
          className="px-4 py-2 bg-green-500 text-white rounded hover:bg-green-600"
        >
          🔄 立即执行
        </Button>
        <Button
          onClick={handleSave}
          disabled={saving}
          className="px-4 py-2 bg-blue-500 text-white rounded hover:bg-blue-600 disabled:opacity-50"
        >
          {saving ? '保存中...' : '💾 保存修改'}
        </Button>
      </div>
    </div>
  );
};
```

**useSchedulerConfig.ts** - Hook函数

```typescript
import { useState, useEffect, useCallback } from 'react';
import { schedulerAPI } from '../api/scheduler';

export const useSchedulerConfig = () => {
  const [tasks, setTasks] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // 获取所有任务配置
  const fetchTasks = useCallback(async () => {
    setLoading(true);
    try {
      const data = await schedulerAPI.getAllConfigs();
      setTasks(data);
      setError(null);
    } catch (err) {
      setError((err as any).message);
    } finally {
      setLoading(false);
    }
  }, []);

  // 获取单个任务配置
  const getTaskConfig = useCallback(
    async (taskName: string) => {
      return await schedulerAPI.getConfig(taskName);
    },
    []
  );

  // 更新任务配置
  const updateTask = useCallback(
    async (taskName: string, updates: any) => {
      try {
        await schedulerAPI.updateConfig(taskName, updates);
        // 刷新列表
        await fetchTasks();
      } catch (err) {
        setError((err as any).message);
        throw err;
      }
    },
    [fetchTasks]
  );

  // 初始加载
  useEffect(() => {
    fetchTasks();
    // 每30秒自动刷新一次
    const interval = setInterval(fetchTasks, 30000);
    return () => clearInterval(interval);
  }, [fetchTasks]);

  return {
    tasks,
    loading,
    error,
    fetchTasks,
    getTaskConfig,
    updateTask,
  };
};
```

---

## 4. API集成

### 4.1 API客户端 (src/features/scheduler/api/scheduler.ts)

```typescript
import { apiClient } from '@/lib/api';

export const schedulerAPI = {
  // 获取所有配置
  getAllConfigs: async () => {
    const response = await apiClient.get('/api/admin/scheduler/config');
    return response.data.data;
  },

  // 获取单个配置
  getConfig: async (taskName: string) => {
    const response = await apiClient.get(
      `/api/admin/scheduler/config/${taskName}`
    );
    return response.data.data;
  },

  // 更新配置
  updateConfig: async (taskName: string, updates: any) => {
    const response = await apiClient.put(
      `/api/admin/scheduler/config/${taskName}`,
      updates
    );
    return response.data;
  },

  // 获取任务执行状态
  getTaskStatus: async (taskName?: string) => {
    const response = await apiClient.get('/api/admin/scheduler/status', {
      params: taskName ? { taskName } : {},
    });
    return response.data.data;
  },

  // 获取分布式锁状态
  getLocks: async () => {
    const response = await apiClient.get('/api/admin/scheduler/locks');
    return response.data.data;
  },

  // 手动执行任务
  executeTask: async (taskName: string) => {
    const response = await apiClient.post(
      `/api/admin/scheduler/execute/${taskName}`
    );
    return response.data;
  },

  // 获取执行日志
  getExecutionLogs: async (params: {
    taskName?: string;
    status?: string;
    nodeId?: string;
    limit?: number;
    offset?: number;
  }) => {
    const response = await apiClient.get('/api/admin/scheduler/logs', {
      params,
    });
    return response.data.data;
  },
};
```

---

## 5. 路由配置

### 5.1 添加管理路由 (src/routes/__root.tsx)

```typescript
// 在路由配置中添加管理菜单
const adminMenuItems = [
  {
    label: '定时任务',
    path: '/admin/scheduler',
    icon: '⚙️',
  },
  {
    label: '集群监控',
    path: '/admin/cluster',
    icon: '📡',
  },
  {
    label: '执行日志',
    path: '/admin/logs',
    icon: '📋',
  },
  {
    label: '告警中心',
    path: '/admin/alerts',
    icon: '🔔',
  },
];
```

### 5.2 创建路由文件 (src/routes/_authenticated/admin/)

```typescript
// admin/scheduler/index.tsx
import React from 'react';
import { TaskListTable } from '@/features/scheduler/components/TaskListTable';
import { useAuth } from '@/hooks/useAuth';

export default function SchedulerPage() {
  const { user } = useAuth();

  // 权限检查
  if (!['admin', 'devops'].includes(user?.role)) {
    return <div>无权访问此页面</div>;
  }

  return (
    <div className="p-6">
      <h1 className="text-2xl font-bold mb-6">定时任务管理</h1>
      <TaskListTable />
    </div>
  );
}
```

---

## 6. 实现检查清单

### 第一阶段：后端API (已完成)
- [x] GET `/api/admin/scheduler/config` - 获取所有配置
- [x] GET `/api/admin/scheduler/config/:task_name` - 获取单个
- [x] PUT `/api/admin/scheduler/config/:task_name` - 更新配置
- [x] GET `/api/admin/scheduler/status` - 获取状态
- [x] GET `/api/admin/scheduler/locks` - 获取锁
- [x] POST `/api/admin/scheduler/execute/:task_name` - 手动执行

### 第二阶段：前端UI (需实现)
- [ ] 任务列表页面 + 表格
- [ ] 任务详情编辑页面
- [ ] 集群监控页面
- [ ] 执行日志查询页面
- [ ] 告警中心页面
- [ ] API客户端和Hook
- [ ] 路由配置

### 第三阶段：功能完善
- [ ] 实时刷新（WebSocket或轮询）
- [ ] 批量操作（批量启用/禁用）
- [ ] 配置导入导出
- [ ] 告警规则配置
- [ ] 数据导出（Excel）
- [ ] 权限控制

### 第四阶段：优化
- [ ] 性能优化（虚拟列表）
- [ ] 缓存策略
- [ ] 错误处理完善
- [ ] 加载动画优化
- [ ] 响应式设计

---

## 7. 权限控制

### 7.1 权限矩阵

| 操作 | Super Admin | DevOps | Developer |
|------|-----------|--------|-----------|
| 查看配置 | ✓ | ✓ | ✓ |
| 修改参数 | ✓ | ✓ (部分) | ✗ |
| 启用/禁用 | ✓ | ✓ | ✗ |
| 手动执行 | ✓ | ✓ | ✗ |
| 查看日志 | ✓ | ✓ | ✓ |
| 修改告警 | ✓ | ✓ | ✗ |

### 7.2 权限检查中间件

```typescript
// 在路由中添加权限守卫
const requireAdminPermission = (requiredRole: string[]) => {
  return (context: any) => {
    const user = context.auth?.user;
    if (!user || !requiredRole.includes(user.role)) {
      throw new Error('Insufficient permissions');
    }
  };
};

// 使用
<Route
  path="/admin/scheduler"
  element={<SchedulerPage />}
  beforeLoad={requireAdminPermission(['admin', 'devops'])}
/>
```

---

## 8. 响应式设计

### 移动端适配

```
桌面版 (>1024px)
├─ 三列布局
├─ 完整表格展示
└─ 所有功能可用

平板版 (768px-1024px)
├─ 两列布局
├─ 表格压缩显示
└─ 部分功能隐藏

手机版 (<768px)
├─ 单列布局
├─ 卡片式展示
├─ 抽屉菜单
└─ 简化的操作
```

---

## 9. 实时更新机制

### 9.1 轮询方案 (简单可靠)

```typescript
const [refreshInterval, setRefreshInterval] = useState(30000); // 30秒

useEffect(() => {
  const interval = setInterval(() => {
    fetchTasks();
    fetchClusterStatus();
  }, refreshInterval);

  return () => clearInterval(interval);
}, [refreshInterval]);
```

### 9.2 WebSocket方案 (实时性更好)

```typescript
// 如果需要更实时的更新，可以使用WebSocket
const ws = new WebSocket('ws://localhost:8080/ws/scheduler');

ws.onmessage = (event) => {
  const update = JSON.parse(event.data);
  if (update.type === 'task_status_changed') {
    // 更新任务状态
    updateTaskStatus(update.taskName, update.status);
  }
};
```

---

## 10. 总结

### 核心特性

✅ **完整的任务管理UI**
- 查看所有任务
- 编辑任务参数（启用、间隔、超时）
- 立即执行任务
- 查看执行历史

✅ **集群监控面板**
- 节点列表和状态
- 分布式锁分配情况
- Master选举状态

✅ **执行日志查询**
- 日志搜索和筛选
- 错误详情查看
- 性能指标展示

✅ **告警和通知**
- 任务失败告警
- 锁超时告警
- 告警历史查看

### 用户体验

🎯 **易用性**
- 无需命令行操作
- 点击式参数配置
- 实时反馈

🎯 **可视性**
- 一目了然的任务状态
- 集群拓扑展示
- 执行趋势图表

🎯 **可控性**
- 灵活的参数调整
- 手动执行任务
- 完整的操作日志

---

**预计工作量**: 前端UI实现 2-3周

**下一步**: 按检查清单逐步实现前端组件和页面

