import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

/** 超级管理员诊断响应；仅包含上游响应及错误/用量证据，不含请求采集内容。 */
interface ClaudeDiagnostic {
  attempt?: number // 中转尝试编号；历史记录可能缺失。
  status_code?: number // 上游 HTTP 状态码；未取得响应时为 0。
  response_headers: Record<string, string[]> // 保留多值的上游响应头，受后端 16 KiB 预算限制。
  headers_truncated: boolean // 响应头是否因预算限制省略部分值。
  body_head_base64: string // 已读 body 前 1024 字节的 base64 编码。
  body_tail_base64: string // 首部之后的最后 1024 字节的 base64 编码。
  observed_bytes: number // 实际读取字节数，不等于未读取部分也已获取。
  omitted_bytes: number // 被省略的中间字节数；0 表示首尾拼接即为全部已读内容。
  read_eof: boolean // 底层是否观察到 EOF，与协议完整结束分别判断。
  error?: string // 私有终止原因，可能来自协议校验或传输错误。
  usage_evidence: Record<string, number> // 上游确认的累计 token/工具次数，键缺失与显式 0 有区别。
  usage_final: boolean // 输出报告之后没有新内容块，且消息结束校验通过。
  settlement_error?: string // 资金或令牌结算错误，与响应处理错误分开记录。
  read_error?: string // 该 HTTP 交换的底层读取/传输错误。
  reject_reason?: string // 上游策略停止原因，仅本私有详情展示。
  usage_phases?: Record<string, string> // 各确认字段最近来自哪个事件阶段。
  estimated_usage?: Record<string, number> // 本地估算 token，独立于上游原始证据。
  previous_responses?: ClaudeDiagnostic[] // 同一尝试较早保留的 SDK 响应；与当前响应合计最多四份。
  omitted_responses?: number // 超出保留上限的历史交换数量。
}

/**
 * decodeBody 将保留的原始字节解码为展示文本，不还原已省略的中间数据。
 * @param head 首部 base64；空字符串按空字节处理。
 * @param tail 尾部 base64，缺省为空；未截断时与首部按字节拼接后再解码，避免 UTF-8 边界被拆开。
 * @returns UTF-8 展示文本；二进制或被截断的多字节字符可能显示替代字符，原始 base64 仍保留原字节。
 */
function decodeBody(head: string, tail = ''): string {
  // 映射回调 c 是 atob 得到的单字节字符，charCodeAt(0) 恢复对应原始字节。
  const first = Uint8Array.from(atob(head || ''), (c) => c.charCodeAt(0))
  const last = Uint8Array.from(atob(tail || ''), (c) => c.charCodeAt(0))
  const all = new Uint8Array(first.length + last.length)
  all.set(first)
  all.set(last, first.length)
  return new TextDecoder().decode(all)
}

/**
 * ClaudeDiagnosticPanel 按需加载超级管理员原始响应诊断；普通账号返回 null。
 * @param props 请求标识、日志创建时间和中转尝试编号，共同定位本条日志。
 * @returns 诊断按钮、加载/错误状态及原始响应详情。组件只在详情弹窗打开时挂载，最后一个观察者卸载后立即清理查询缓存。
 */
export function ClaudeDiagnosticPanel(props: {
  requestId: string // 请求 ID，区别不同请求。
  createdAt: number // 日志创建时间，Unix 秒，与后端精确匹配。
  attempt: number // 中转尝试编号；历史记录使用 0。
}) {
  const { t } = useTranslation()
  // 选择器 s 为认证状态快照；账号 ID 用于隔离查询缓存，role 用于展示门槛，后端仍执行 RootAuth。
  const user = useAuthStore((s) => s.auth.user)
  const [requested, setRequested] = useState(false) // 用户首次主动点击后才请求私有数据。
  const query = useQuery({
    // 同一请求的不同重试及不同登录账号分别缓存，避免混用诊断。
    queryKey: ['claude-diagnostic', user?.id, props.requestId, props.createdAt, props.attempt],
    enabled: requested && (user?.role ?? 0) >= 100, // 仅用户主动请求且为超级管理员时启用。
    retry: false, // 查询失败由用户明确重试，不后台重复读取私有数据。
    gcTime: 0, // 最后一个组件观察者卸载后立即清除缓存。
    refetchOnWindowFocus: false, // 切换窗口焦点不自动重读诊断。
    // queryFn 的 signal 为 React Query 取消信号，透传 HTTP 层；返回诊断数据，失败抛错交给面板展示。
    queryFn: async ({ signal }) => {
      const response = await api.get<{
        success: boolean
        data?: ClaudeDiagnostic
      }>('/api/log/claude-diagnostic', {
        params: { request_id: props.requestId, created_at: props.createdAt, attempt: props.attempt },
        signal,
      })
      if (!response.data.success || !response.data.data) {
        throw new Error('Diagnostic lookup failed')
      }
      return response.data.data
    },
  })
  if ((user?.role ?? 0) < 100) return null
  const detail = query.data
  let body = ''
  if (detail) {
    body = decodeBody(detail.body_head_base64, detail.body_tail_base64)
    if (detail.omitted_bytes > 0) {
      body = `${decodeBody(detail.body_head_base64)}\n\n${t('Omitted {{count}} bytes', { count: detail.omitted_bytes })}\n\n${decodeBody(detail.body_tail_base64)}`
    }
  }
  return (
    <div className='min-w-0 space-y-2'>
      <Button
        variant='outline'
        size='sm'
        disabled={query.isFetching}
        onClick={() => {
          // 无参点击回调：首次启用查询，后续点击刷新当前请求/尝试的诊断。
          if (requested) void query.refetch()
          else setRequested(true)
        }}
      >
        {query.isFetching
          ? t('Loading...')
          : t('Upstream diagnostics (Root only)')}
      </Button>
      {query.isError && (
        <p className='text-destructive text-xs' role='alert'>
          {t('Diagnostics could not be loaded. Please retry.')}
        </p>
      )}
      {detail && (
        <div className='bg-muted/30 min-w-0 space-y-2 rounded-md border p-2.5'>
          <p className='text-muted-foreground text-xs'>
            {t('Observed {{count}} bytes', { count: detail.observed_bytes ?? 0 })}
            {' · '}
            {detail.read_eof
              ? t('Upstream EOF observed')
              : t('Only bytes read are shown')}
          </p>
          {detail.headers_truncated && (
            <p className='text-xs'>{t('Response headers were truncated')}</p>
          )}
          <p className='text-xs font-semibold'>
            {t('Upstream response headers')}
          </p>
          <pre className='bg-background/60 max-h-48 overflow-auto rounded border p-2 font-mono text-xs break-all whitespace-pre-wrap'>
            {JSON.stringify(detail.response_headers, null, 2)}
          </pre>
          <p className='text-xs font-semibold'>{t('Upstream response body')}</p>
          <pre className='bg-background/60 max-h-72 overflow-auto rounded border p-2 font-mono text-xs break-all whitespace-pre-wrap'>
            {body}
          </pre>
          <p className='text-xs font-semibold'>
            {t('Error and usage evidence')}
          </p>
          <pre className='bg-background/60 max-h-48 overflow-auto rounded border p-2 font-mono text-xs break-all whitespace-pre-wrap'>
            {JSON.stringify(
              {
                error: detail.error,
                read_error: detail.read_error,
                attempt: detail.attempt,
                status_code: detail.status_code,
                reject_reason: detail.reject_reason,
                usage_evidence: detail.usage_evidence,
                usage_phases: detail.usage_phases,
                estimated_usage: detail.estimated_usage,
                usage_final: detail.usage_final,
                settlement_error: detail.settlement_error,
                omitted_responses: detail.omitted_responses,
              },
              null,
              2
            )}
          </pre>
          {!!detail.previous_responses?.length && (
            <>
              <p className='text-xs font-semibold'>{t('Previous upstream responses')}</p>
              <pre className='bg-background/60 max-h-72 overflow-auto rounded border p-2 font-mono text-xs break-all whitespace-pre-wrap'>
                {JSON.stringify(detail.previous_responses, null, 2)}
              </pre>
            </>
          )}
        </div>
      )}
    </div>
  )
}
