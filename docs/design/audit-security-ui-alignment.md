# 安全监控页面与原有审计风格对齐

2026-11-16。用户指出已上线的安全监控页面不符合项目原有风格。本次是现有页面的样式及组件复用修正，按用户要求直接修复，保持 API、权限和告警处理合同。

2026-11-17 后续调整见[审计详情、导出权限与安全告警清理](audit-log-usability-and-export-access.md)：详情改为宽屏四分区，新增单条删除、已关闭告警批量清理及独立删除授权；本文件保留此前列表对齐的实施记录。

同日按用户再次确认，监控设置已落实原设计要求的右侧 Sheet，补齐 IP 缓存时长和参数说明；桌面/手机实际保存及取消回归见[确认修复记录](release-frontend-fixes-20261117.md)。此前列表对齐并未证明设置弹层符合设计，不能沿用其验收结论代替本次检查。

## Existing Logic and Related-Path Findings

- 普通审计页 `components/audit-log-viewer.tsx` 使用 `useDataTable` / `DataTablePage`：表头、表体滚动、手机卡片、列显示控制统一；`audit-log-filter-bar.tsx` 组合 `LogsFilterToolbar`，默认常用筛选，其他项展开，手机使用抽屉。
- `security/monitor.tsx` 却直接使用 `StaticDataTable`，把全部筛选排成带标签的四列大表单，外层 `overflow-auto` 使页面整体横向滚动。此静态表不适合管理列表。
- 监控健康信息在 `AlertDescription` 中连续输出多个 span，缺少布局间隔，还残留问号分隔符；源状态表始终占据主视图。字段来自 Root-only health，须保留其可见性边界。
- 列表有 ID/activity 两类游标，recent_hits 固定 20 条；capacity 是有界统计，不能充当精确分页总数。继续保留已有上一页/下一页，不伪造总页数。
- 设置、导出、行内状态修改、详情与审计回查分别复用原有组件；更新筛选重置游标，文本通过查询提交，分页固定时间窗，保持不变。

## 修改范围及交互

1. 复用原审计 `LogsFilterToolbar` / `LogsFilterField` / `LogsFilterInput`。常用时间、视图、资源和状态默认展示；规则、风险、证据口径、IP、操作者和负责人放展开区。手机复用该工具栏抽屉。保留所有筛选，补齐同款查询/重置/列显示按钮。
2. 列表改为 `DataTablePage` 与 feature 内的 TanStack columns，复用普通审计的字号、行间距和移动卡片；滚动限制在表格，不让整个页面横向溢出。状态操作仍在列表。
3. 健康区默认展示监控状态、上次扫描；其余运行细节和数据源表放可展开区域，有异常时在摘要明确显示。展开后字段按独立项排版，删除错误问号分隔符，不删减诊断信息。
4. 统计与说明使用工具栏 stats / 列表辅助文字位置，避免叠加多块占高区域。所有颜色继续来自主题 tokens。

## 生命周期、API 与资源

保持现有 React Query 查询键、鉴权、AbortSignal、刷新、游标、导出及保存逻辑；健康仍仅 Root 请求/显示。展开状态仅组件内存；列可见性使用共享表格能力，不新增持久业务状态。不新增接口、表、迁移、后台配置、第三方请求或依赖。

Main Chain Impact：同步 relay 工作为零；不修改 Go、数据库池、Redis、审计 worker 或外部源。新增的表格状态仅当前浏览器页面所有，不与主链共享资源。

## 验证

复跑已有审计交互测试（分页固定时间窗、权限、状态冲突、详情和设置二次验证），补充展开/重置/隐藏字段提交相关回归。执行实际 TypeScript 编译器、定向 lint 和生产构建。通过浏览器比对原审计页与安全页，检查桌面/窄屏、展开区、下拉选择、状态操作及主题，记录实际执行边界。发布从当前精确版本快照仅纳入本次前端差异，保留回滚版本。

## 实际实现

2026-11-16 完成。`monitor.tsx` 组合三个 feature 内组件：`security-filter-bar.tsx`、`security-monitor-health.tsx`、`security-alert-columns.tsx`。复用 `LogsFilterToolbar` 的 `compactMobile` 让手机操作按钮换行；复用表格 `meta.pinned: 'right'` 固定状态操作，横向滚动只发生在表格内部。健康详情默认收起，但存储不可读、监控或数据源异常仍在摘要提示。没有新增翻译键或修改共享组件。

### 2026-11-22 统一分页修正

安全监控与客户审计共用列表布局和分页。调整前，表格已经复用 `DataTablePage`，但筛选栏在其外部，`showPagination={false}`，页底单独拼装前后页按钮。`controller/securityAlertFilter` 的默认页长为 50，ID、未结束、已归档视图使用 `before`，最近更新使用绑定筛选条件的 `cursor`；最近命中只返回 20 条，没有后续页。`capacity` 仍非列表精确总数。

本次把筛选/健康栏传入 `DataTablePage.toolbar`，由公共布局管理固定高度与表格滚动；采用与客户审计共用的 `DataTablePagination` 游标模式，通过 `PageFooterPortal` 放入页面底部。显示本页条数、每页 50 条、当前页、首页/前后页；最近命中继续保留其 20 条摘要语义，不提供无效翻页。更新筛选、搜索、清理后刷新仍重置第一页，告警处理仍失效原查询，后台刷新不重置滚动位置。

公共分页兼容扩展及两条新增文案见 [客户审计统一列表与分页](employee-customer-audit-default-list.md)。没有改变后端查询、API 参数、权限、总数计算、缓存作用域或 relay 共享资源。验收包含数字/字符串游标、移动端滚动、筛选重置、状态处理与页脚显示。
