# new-api E2E Test Report

Generated: 2026-07-21T06:05:12.323Z

Backend :3000 (real MySQL/PG/Redis) · Default UI :3002 · Classic UI :5173 · Playwright/Chromium

Accounts: e2e_root (root/100), e2e_admin (admin/10), e2e_common (common/1), all password `Test1234!`; guest = unauthenticated.

## Summary

- Smoke pages tested: **244** — PASS **242**, FAIL **2**
- RBAC checks: 44 · Login checks: 4 · CRUD flows: 14

### Smoke pass/fail by UI + role

| UI | Role | Pass | Fail | Total |
|---|---|---|---|---|
| classic | admin | 32 | 0 | 32 |
| classic | common | 24 | 0 | 24 |
| classic | employee | 7 | 0 | 7 |
| classic | guest | 10 | 0 | 10 |
| classic | root | 31 | 1 | 32 |
| default | admin | 41 | 0 | 41 |
| default | common | 32 | 0 | 32 |
| default | employee | 8 | 0 | 8 |
| default | guest | 17 | 0 | 17 |
| default | root | 40 | 1 | 41 |

## Smoke FAILURES (2)

| UI | Role | Route | finalUrl | blank? | pageErrors | 5xx | note |
|---|---|---|---|---|---|---|---|
| classic | root | `/console/node-pool` | /console/node-pool | no | - | 503 http://127.0.0.1:5173/api/node-pool/nodes | JL API 控制台 模型广场 E e2e_root 聊天 操练场 聊天 控制台 数据看板 令牌管理 使用日志 绘图日志 任务日志 个人中心 钱包管理 个人设置 |
| default | root | `/node-pool` | /node-pool | no | - | 503 http://127.0.0.1:3002/api/node-pool/nodes | Skip to Main Toggle Sidebar JL API Console Model Square Rankings Search ⌘ K Chan |

## Pages with console errors (non-fatal, 38)

| UI | Role | Route | console.error (first) |
|---|---|---|---|
| classic | common | `/chat2link` | 当前没有可用的启用令牌，请确认是否有令牌处于启用状态！ |
| classic | admin | `/chat2link` | 当前没有可用的启用令牌，请确认是否有令牌处于启用状态！ |
| classic | common | `/console` | %o

%s

%s
 TypeError: Cannot read properties of undefined (reading 'createCanvas')
    at DefaultGlobal.createCanvas (http://127.0.0.1:5173 |
| classic | admin | `/console` | %o

%s

%s
 TypeError: Cannot read properties of undefined (reading 'createCanvas')
    at DefaultGlobal.createCanvas (http://127.0.0.1:5173 |
| classic | root | `/console` | %o

%s

%s
 TypeError: Cannot read properties of undefined (reading 'createCanvas')
    at DefaultGlobal.createCanvas (http://127.0.0.1:5173 |
| classic | employee | `/console` | %o

%s

%s
 TypeError: Cannot read properties of undefined (reading 'createCanvas')
    at DefaultGlobal.createCanvas (http://127.0.0.1:5173 |
| classic | common | `/console/chat` | 当前没有可用的启用令牌，请确认是否有令牌处于启用状态！ |
| classic | admin | `/console/chat` | 当前没有可用的启用令牌，请确认是否有令牌处于启用状态！ |
| classic | common | `/console/commission` | permission denied |
| classic | admin | `/console/commission` | permission denied |
| classic | root | `/console/commission` | permission denied |
| classic | common | `/console/customer-console` | current user is not an enabled employee |
| classic | admin | `/console/customer-console` | current user is not an enabled employee |
| classic | root | `/console/customer-console` | current user is not an enabled employee |
| classic | root | `/console/node-pool` | Failed to load resource: the server responded with a status of 503 (Service Unavailable) |
| classic | common | `/console/personal` | Received `%s` for a non-boolean attribute `%s`.

If you want to write it to the DOM, pass a string instead: %s="%s" or %s={value.toString()} |
| classic | admin | `/console/personal` | Received `%s` for a non-boolean attribute `%s`.

If you want to write it to the DOM, pass a string instead: %s="%s" or %s={value.toString()} |
| classic | employee | `/console/personal` | Received `%s` for a non-boolean attribute `%s`.

If you want to write it to the DOM, pass a string instead: %s="%s" or %s={value.toString()} |
| classic | root | `/console/setting` | Received `%s` for a non-boolean attribute `%s`.

If you want to write it to the DOM, pass a string instead: %s="%s" or %s={value.toString()} |
| classic | common | `/login` | %o

%s

%s
 TypeError: Cannot read properties of undefined (reading 'createCanvas')
    at DefaultGlobal.createCanvas (http://127.0.0.1:5173 |
| classic | admin | `/login` | %o

%s

%s
 TypeError: Cannot read properties of undefined (reading 'createCanvas')
    at DefaultGlobal.createCanvas (http://127.0.0.1:5173 |
| classic | root | `/login` | %o

%s

%s
 TypeError: Cannot read properties of undefined (reading 'createCanvas')
    at DefaultGlobal.createCanvas (http://127.0.0.1:5173 |
| classic | guest | `/pricing` | Received `%s` for a non-boolean attribute `%s`.

If you want to write it to the DOM, pass a string instead: %s="%s" or %s={value.toString()} |
| classic | common | `/pricing` | Received `%s` for a non-boolean attribute `%s`.

If you want to write it to the DOM, pass a string instead: %s="%s" or %s={value.toString()} |
| classic | admin | `/pricing` | Received `%s` for a non-boolean attribute `%s`.

If you want to write it to the DOM, pass a string instead: %s="%s" or %s={value.toString()} |
| classic | root | `/pricing` | Received `%s` for a non-boolean attribute `%s`.

If you want to write it to the DOM, pass a string instead: %s="%s" or %s={value.toString()} |
| classic | guest | `/privacy-policy` | 加载隐私政策内容失败... |
| classic | common | `/privacy-policy` | 加载隐私政策内容失败... |
| classic | admin | `/privacy-policy` | 加载隐私政策内容失败... |
| classic | root | `/privacy-policy` | 加载隐私政策内容失败... |
| classic | common | `/register` | %o

%s

%s
 TypeError: Cannot read properties of undefined (reading 'createCanvas')
    at DefaultGlobal.createCanvas (http://127.0.0.1:5173 |
| classic | admin | `/register` | %o

%s

%s
 TypeError: Cannot read properties of undefined (reading 'createCanvas')
    at DefaultGlobal.createCanvas (http://127.0.0.1:5173 |
| classic | root | `/register` | %o

%s

%s
 TypeError: Cannot read properties of undefined (reading 'createCanvas')
    at DefaultGlobal.createCanvas (http://127.0.0.1:5173 |
| classic | guest | `/user-agreement` | 加载用户协议内容失败... |
| classic | common | `/user-agreement` | 加载用户协议内容失败... |
| classic | admin | `/user-agreement` | 加载用户协议内容失败... |
| classic | root | `/user-agreement` | 加载用户协议内容失败... |
| default | root | `/node-pool` | Failed to load resource: the server responded with a status of 503 (Service Unavailable) |

## Full smoke matrix

### default

| Route | guest | common | employee | admin | root |
|---|---|---|---|---|---|
| `/` | PASS | PASS | - | PASS | PASS |
| `/401` | PASS | PASS | - | PASS | PASS |
| `/403` | PASS | PASS | - | PASS | PASS |
| `/404` | PASS | PASS | - | PASS | PASS |
| `/500` | PASS | PASS | - | PASS | PASS |
| `/503` | PASS | PASS | - | PASS | PASS |
| `/about` | PASS | PASS | - | PASS | PASS |
| `/channels` | - | - | - | PASS | PASS |
| `/commission` | - | PASS | PASS | PASS | PASS |
| `/commission-overview` | - | PASS | PASS | PASS | PASS |
| `/console/log` | - | PASS | - | PASS | PASS |
| `/console/topup` | - | PASS | - | PASS | PASS |
| `/customer-console` | - | PASS | PASS | PASS | PASS |
| `/customers` | - | - | PASS | PASS | PASS |
| `/dashboard` | - | PASS | PASS | PASS | PASS |
| `/employees` | - | - | - | PASS | PASS |
| `/forgot-password` | PASS | PASS | - | PASS | PASS |
| `/keys` | - | PASS | PASS | PASS | PASS |
| `/marketplace` | - | PASS | - | PASS | PASS |
| `/models` | - | - | - | PASS | PASS |
| `/node-pool` | - | - | - | PASS | FAIL |
| `/otp` | PASS | PASS | - | PASS | PASS |
| `/playground` | - | PASS | - | PASS | PASS |
| `/pricing` | PASS | PASS | - | PASS | PASS |
| `/privacy-policy` | PASS | PASS | - | PASS | PASS |
| `/profile` | - | PASS | PASS | PASS | PASS |
| `/rankings` | PASS | PASS | - | PASS | PASS |
| `/redemption-codes` | - | - | - | PASS | PASS |
| `/register` | PASS | PASS | - | PASS | PASS |
| `/request-logs` | - | PASS | - | PASS | PASS |
| `/reset` | PASS | PASS | - | PASS | PASS |
| `/security` | - | PASS | - | PASS | PASS |
| `/sign-in` | PASS | PASS | - | PASS | PASS |
| `/sign-up` | PASS | PASS | - | PASS | PASS |
| `/subscriptions` | - | PASS | - | PASS | PASS |
| `/system-info` | - | - | - | PASS | PASS |
| `/system-settings` | - | - | - | PASS | PASS |
| `/usage-logs` | - | PASS | - | PASS | PASS |
| `/user-agreement` | PASS | PASS | - | PASS | PASS |
| `/users` | - | - | - | PASS | PASS |
| `/wallet` | - | PASS | PASS | PASS | PASS |

### classic

| Route | guest | common | employee | admin | root |
|---|---|---|---|---|---|
| `/` | PASS | PASS | - | PASS | PASS |
| `/about` | PASS | PASS | - | PASS | PASS |
| `/chat2link` | PASS | PASS | - | PASS | PASS |
| `/console` | - | PASS | PASS | PASS | PASS |
| `/console/channel` | - | - | - | PASS | PASS |
| `/console/chat` | - | PASS | - | PASS | PASS |
| `/console/commission` | - | PASS | PASS | PASS | PASS |
| `/console/commission-overview` | - | PASS | PASS | PASS | PASS |
| `/console/customer-console` | - | PASS | PASS | PASS | PASS |
| `/console/deployment` | - | - | - | PASS | PASS |
| `/console/employees` | - | - | - | PASS | PASS |
| `/console/log` | - | PASS | PASS | PASS | PASS |
| `/console/midjourney` | - | PASS | - | PASS | PASS |
| `/console/models` | - | - | - | PASS | PASS |
| `/console/node-pool` | - | - | - | PASS | FAIL |
| `/console/personal` | - | PASS | PASS | PASS | PASS |
| `/console/playground` | - | PASS | - | PASS | PASS |
| `/console/redemption` | - | - | - | PASS | PASS |
| `/console/request-log` | - | PASS | - | PASS | PASS |
| `/console/setting` | - | - | - | PASS | PASS |
| `/console/subscription` | - | PASS | - | PASS | PASS |
| `/console/task` | - | PASS | - | PASS | PASS |
| `/console/token` | - | PASS | PASS | PASS | PASS |
| `/console/topup` | - | PASS | - | PASS | PASS |
| `/console/user` | - | - | - | PASS | PASS |
| `/forbidden` | PASS | PASS | - | PASS | PASS |
| `/login` | PASS | PASS | - | PASS | PASS |
| `/pricing` | PASS | PASS | - | PASS | PASS |
| `/privacy-policy` | PASS | PASS | - | PASS | PASS |
| `/register` | PASS | PASS | - | PASS | PASS |
| `/reset` | PASS | PASS | - | PASS | PASS |
| `/user-agreement` | PASS | PASS | - | PASS | PASS |

## RBAC results

Leaks detected: **0**

| UI | kind | route | finalUrl | stayed | forbiddenShown | leak |
|---|---|---|---|---|---|---|
| default | guest | `/channels` | /sign-in?redirect=%2Fchannels | - | true | - |
| default | guest | `/users` | /sign-in?redirect=%2Fusers | - | true | - |
| default | guest | `/system-settings` | /sign-in?redirect=%2Fsystem-settings | - | true | - |
| default | guest | `/keys` | /sign-in?redirect=%2Fkeys | - | true | - |
| default | guest | `/dashboard` | /sign-in?redirect=%2Fdashboard | - | true | - |
| classic | guest | `/console/channel` | /login?expired=true | - | true | - |
| classic | guest | `/console/user` | /login?expired=true | - | true | - |
| classic | guest | `/console/setting` | /login?expired=true | - | true | - |
| classic | guest | `/console/token` | /login?expired=true | - | true | - |
| classic | guest | `/console` | /login?expired=true | - | true | - |
| default | common-admin | `/channels` | /403 | false | true | - |
| default | common-admin | `/users` | /403 | false | true | - |
| default | common-admin | `/models` | /403 | false | true | - |
| default | common-admin | `/node-pool` | /403 | false | true | - |
| default | common-admin | `/employees` | /403 | false | true | - |
| default | common-admin | `/customers` | /403 | false | true | - |
| default | common-admin | `/redemption-codes` | /403 | false | true | - |
| default | common-admin | `/system-settings` | /403 | false | true | - |
| default | common-admin | `/system-info` | /403 | false | true | - |
| classic | common-admin | `/console/channel` | /forbidden | false | true | - |
| classic | common-admin | `/console/user` | /forbidden | false | true | - |
| classic | common-admin | `/console/setting` | /forbidden | false | true | - |
| classic | common-admin | `/console/redemption` | /forbidden | false | true | - |
| classic | common-admin | `/console/models` | /forbidden | false | true | - |
| classic | common-admin | `/console/deployment` | /forbidden | false | true | - |
| classic | common-admin | `/console/node-pool` | /forbidden | false | true | - |
| classic | common-admin | `/console/employees` | /forbidden | false | true | - |
| default | employee-admin | `/channels` | /403 | false | - | - |
| default | employee-admin | `/users` | /403 | false | - | - |
| default | employee-admin | `/models` | /403 | false | - | - |
| default | employee-admin | `/node-pool` | /403 | false | - | - |
| default | employee-admin | `/employees` | /403 | false | - | - |
| default | employee-admin | `/customers` | /403 | false | - | - |
| default | employee-admin | `/redemption-codes` | /403 | false | - | - |
| default | employee-admin | `/system-settings` | /403 | false | - | - |
| default | employee-admin | `/system-info` | /403 | false | - | - |
| classic | employee-admin | `/console/channel` | /forbidden | false | - | - |
| classic | employee-admin | `/console/user` | /forbidden | false | - | - |
| classic | employee-admin | `/console/setting` | /forbidden | false | - | - |
| classic | employee-admin | `/console/redemption` | /forbidden | false | - | - |
| classic | employee-admin | `/console/models` | /forbidden | false | - | - |
| classic | employee-admin | `/console/deployment` | /forbidden | false | - | - |
| classic | employee-admin | `/console/node-pool` | /forbidden | false | - | - |
| classic | employee-admin | `/console/employees` | /forbidden | false | - | - |

## Login flows

| UI | case | finalUrl | result |
|---|---|---|---|
| default | invalid | /sign-in | PASS |
| default | valid | /dashboard/overview | PASS |
| classic | invalid | /login | PASS |
| classic | valid | /console | PASS |

## Key CRUD flows

| UI | flow | result | detail |
|---|---|---|---|
| backend | channel.create | PASS | status=200 msg= name=e2ecrudA_chan |
| backend | token.create | PASS | status=200 msg= name=e2ecrudA_tok |
| backend | user.create | PASS | status=200 msg= name=e2ecrudA_usr |
| backend | redemption.create | PASS | status=200 msg= name=e2ecrudA_rdm |
| backend | setting.toggle | PASS | key=DataExportEnabled true->false->restore put=true restore=true |
| default | list.row /channels | FAIL | needle=e2ecrudA_chan found=0 |
| default | list.row /keys | FAIL | needle=e2ecrudA_tok found=0 |
| default | list.row /users | FAIL | needle=e2ecrudA_usr found=0 |
| default | list.row /redemption-codes | FAIL | needle=e2ecrudA_rdm found=0 |
| classic | list.row /console/channel | FAIL | needle=e2ecrudA_chan found=0 |
| classic | list.row /console/token | FAIL | needle=e2ecrudA_tok found=0 |
| classic | list.row /console/user | FAIL | needle=e2ecrudA_usr found=0 |
| classic | list.row /console/redemption | FAIL | needle=e2ecrudA_rdm found=0 |
| backend | cleanup | PASS | channel:1 token:0 user:1 redemption:1 |

## Employee role (B)

Seeded `e2e_emp` = common user (role 1) + `employee_profiles` row (status=1) bound to tier (rate 0.10). Employee-ness is via employee_profiles, not role, so RBAC treats it as a common user for admin pages.

- Employee smoke pages: 15/15 passed.
- Employee blocked from admin pages: 17/17 correctly blocked, 0 leaks.
- Employee self commission summary (via their own session): performance(profit)=1116, commission_total(accrual)=122, current_commission(period)=112, customer_consumption=2256 — matches DB ground truth.

## 员工 / 台账 / 提成 data correctness (C)

### Seed

- **employee**: e2e_emp (user_id 830000075), employee_profiles id 621 status=1, tier_id 1647 rate 0.10
- **customer**: e2e_cust (user_id 830000076), inviter_id=830000075 (=E), quota 1e9, group default
- **channel**: e2e_mockai (id 830000083) type OpenAI -> http://127.0.0.1:18080, cost_ratio 0.50, group default
- **token**: e2e_cust_tok (id 8879) unlimited quota
- **tier_rate**: 0.1
- **cost_ratio**: 0.5
- **group_ratio**: 1

### Known consumption driven

- N = **61** requests as customer C through the :3000 relay (1 warmup + 60 loop, all HTTP 200 through :3000 relay).

### DB ground truth

```json
{
  "logdb_postgres_logs_C": {
    "count": 61,
    "sum_quota": 2256
  },
  "users_C_used_quota": 2256,
  "consumption_costs_C": {
    "count": 61,
    "sum_revenue": 2256,
    "sum_cost": 1140,
    "per_row_cost_formula_mismatches": 0
  },
  "employee_commission_logs": {
    "count": 61,
    "sum_revenue": 2256,
    "sum_cost": 1140,
    "sum_profit": 1116,
    "sum_commission": 122,
    "per_row_commission_formula_mismatches": 0
  },
  "daily_stats_E": {
    "sum_revenue": 2256,
    "sum_cost": 1140,
    "sum_profit": 1116,
    "sum_commission": 122,
    "sum_record_count": 61
  }
}
```

### Expected formulas

- **cost**: round(revenue / group_ratio * cost_ratio) = round(2256/1*0.5) per-row -> 1140
- **profit**: revenue - cost = 2256 - 1140 = 1116
- **commission_accrual**: SUM per-row round(profit*0.10) min1 = 122
- **commission_display_current**: round(SUM(profit)*0.10) = round(111.6) = 112

### Reconciliation (DB vs formula)

| Check | Result |
|---|---|
| used_quota == logdb.sum(quota) == cc.revenue | 2256 == 2256 == 2256  MATCH |
| cc.revenue == commissionLogs.revenue | 2256 == 2256  MATCH |
| profit == revenue - cost | 1116 == 1116  MATCH |
| dailyStats.profit == commissionLogs.profit | 1116 == 1116  MATCH |
| dailyStats.commission == commissionLogs.commission | 122 == 122  MATCH |
| commission per-row formula | 0 mismatches over 61 rows  MATCH |
| cost per-row formula | 0 mismatches over 61 rows  MATCH |

### API-displayed numbers

```json
{
  "admin_overview_by_channel_e2e_mockai": {
    "consumption_quota": 2256,
    "est_cost_quota": 1140,
    "est_profit_quota": 1116,
    "gross_margin": 0.4947,
    "cost_ratio": 0.5
  },
  "admin_commission_summary_E": {
    "revenue": 2256,
    "cost": 1140,
    "profit": 1116,
    "commission": 122,
    "record_count": 61
  },
  "employee_self_summary_E": {
    "current_performance_quota": 1116,
    "commission_total_quota": 122,
    "current_commission_quota": 112,
    "customer_total_consumption_quota": 2256
  }
}
```

### UI-displayed numbers

- **default_commission_overview**: renders e2e_mockai channel (hasMockChannel=true)
- **classic_commission_overview**: renders e2e_mockai channel (hasMockChannel=true)
- **employee_self_session_numbers**: performance=1116, commission_total=122, current_commission=112, consumption=2256 (Playwright assertions PASS)

### D3 ledger hour-boundary (no double-count)

```json
{
  "marker_row": "user_id 999000001 revenue 1000 cost 400 created_at 1784610000 (exact hour boundary)",
  "spanning_range": "[1784606400,1784613600) 2h span",
  "db_raw_sum": {
    "revenue": 3256,
    "cost": 1540,
    "count": 62
  },
  "endpoint_sliced_result": {
    "revenue": 3256,
    "cost": 1540,
    "count": 62
  },
  "control_range_within_one_hour": {
    "revenue": 2256,
    "cost": 1140,
    "count": 61
  },
  "verdict": "MATCH - boundary row counted exactly once, no double-count. D3 fix holds."
}
```

### Notes

- **commission_122_vs_112**: By design: daily_stats + accrual store SUM(per-row round(profit*rate) min1)=122; the 'current period' display recomputes round(SUM(profit)*rate)=112 (documented in calcCommissionQuota). Both values are exposed with distinct labels and both match independent DB computation. Not a bug.
- **logs_in_separate_db**: Request logs live in LOG_SQL_DSN (Postgres logdb), not the main MySQL newapi DB.

