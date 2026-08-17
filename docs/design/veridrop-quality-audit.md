# Veridrop 检测功能质量复审

## Cancellation handling

When a system-task lease is lost, undispatched detection rows are moved from
`queued` to `cancelled` before the worker exits. The task is then finalized as
failed rather than succeeded, so the scheduler can run a later batch without
leaving misleading successful history or permanently queued records.

## 范围

本轮复审不改变 veridrop-monitor 的检测协议，仅修正当前管理端集成中的配置一致性、查询参数校验、HTTP 边界和报告解析问题。

## 实现修正

- `veridrop_monitor_setting` 使用配置草稿与不可变快照发布。请求和后台任务只读取快照，避免热更新时读写同一全局结构。
- 设置保存复用 `PUT /api/option/group` 原子组保存：字段白名单、类型和跨字段规则通过后，才在事务中持久化并发布快照。
- 结果查询严格校验数字范围、分数区间、布尔值、状态、协议、模式和排序字段；无效参数返回统一参数错误。
- Veridrop HTTP 响应上限为 4 MiB，错误正文最多记录 4 KiB。
- 评分详情必须是可解析且至少包含一个有效检测项的 JSON；检测项状态归一化，所有嵌套证据保持完整。结构化解析上游 JSON 错误，避免转义字符导致消息截断。

## 数据流和 API

现有检测与查询端点、鉴权级别和数据表不变。唯一保存行为调整为将同一次设置编辑作为一个配置组提交，避免逐字段更新出现部分成功。

## 错误处理

无效筛选返回现有 `invalid params` 国际化错误；上游超大响应作为检测调用错误处理；管理端不会显示不可解析的伪评分详情入口。

## Main Chain Impact

这些修正只运行在管理端检测、系统任务、设置保存和浏览器报告生成流程。AI relay 请求协程没有新增同步数据库查询、Redis 操作、锁、goroutine 或外部请求。

## Shared Resource Audit

- `options` 表：仅管理员保存设置时通过既有事务写入；relay 主链不读取本配置模块。
- `channel_veridrop_detections` 表：仅检测任务及管理端查询、清理访问；relay 主链不访问。
- 内存配置：独立 `config.Snapshot[VeridropMonitorSetting]`，不与 relay 设置共享对象。

## 回归测试

- 配置边界、跨字段校验与旧快照不可变。
- 查询参数的非法数字、枚举、分数范围和排序值。
- HTTP 超大响应与错误正文长度。
- 多检测项、状态别名、嵌套数组证据、恶意 HTML、畸形 JSON 和带转义引号的错误信息。
