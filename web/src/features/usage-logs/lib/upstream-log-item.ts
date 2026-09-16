/*
Copyright (C) 2023-2026 QuantumNous

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
import type { UsageLog } from '../data/schema'
import type { UpstreamLogItem } from '../types'

/**
 * 把上游日志条目映射成本站日志的形状，让上游详情直接复用本站日志详情的排版，
 * 而不是另写一份迟早会和左侧走样的简版。
 *
 * 目标类型是日志表使用的 schema 版 UsageLog：字段全部必填，other 为 JSON 字符串。
 * 上游没有给出渠道时映射为 0，排版据此隐藏「渠道」一行，而不是渲染出 undefined。
 */
export function upstreamItemToUsageLog(item: UpstreamLogItem): UsageLog {
  return {
    id: item.id,
    user_id: 0,
    token_id: 0,
    username: '',
    created_at: item.created_at,
    type: item.type,
    content: item.content ?? '',
    request_id: item.request_id,
    upstream_request_id: item.upstream_request_id ?? '',
    model_name: item.model_name,
    token_name: item.token_name ?? '',
    quota: item.quota,
    prompt_tokens: item.prompt_tokens,
    completion_tokens: item.completion_tokens,
    use_time: item.use_time,
    is_stream: item.is_stream,
    channel: item.channel ?? 0,
    channel_name: item.channel_name ?? '',
    group: item.group ?? '',
    ip: item.ip ?? '',
    other: item.other ? JSON.stringify(item.other) : '',
  }
}
