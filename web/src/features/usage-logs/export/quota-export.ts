export interface QuotaExportJobFilters {
  user_id?: number
  username?: string
  model_name?: string
  use_group?: string
  channel_id?: number
  token_id?: number
  node_name?: string
}

export interface QuotaExportDraft {
  start: number
  end: number
  userId: string
  username: string
  modelName: string
  useGroup: string
  channelId: string
  tokenId: string
  nodeName: string
  header: boolean
  csvBom: boolean
  timezone: string
  /** Root is the only role that may send token and node filters. */
  root: boolean
}

export interface QuotaExportRequest {
  filters: QuotaExportJobFilters & {
    start_timestamp: number
    end_timestamp: number
  }
  options: {
    header: boolean
    csv_bom: boolean
    timezone: string
  }
}

function runeCount(value: string): number {
  return [...value].length
}

function optionalInteger(raw: string): number | undefined | false {
  const text = raw.trim()
  if (text === '') return undefined
  if (!/^\d+$/.test(text)) return false
  const value = Number(text)
  if (!Number.isSafeInteger(value)) return false
  return value
}

function optionalText(raw: string): string | undefined | false {
  const text = raw.trim()
  if (text === '') return undefined
  if (runeCount(text) > 64) return false
  return text
}

/** Build the strict quota export body. Empty integers are omitted; "0" matches stored zero. */
export function buildQuotaExportRequest(
  draft: QuotaExportDraft
): QuotaExportRequest | undefined {
  const userId = optionalInteger(draft.userId)
  const channelId = optionalInteger(draft.channelId)
  const username = optionalText(draft.username)
  const modelName = optionalText(draft.modelName)
  const useGroup = optionalText(draft.useGroup)
  const tokenId = draft.root ? optionalInteger(draft.tokenId) : undefined
  const nodeName = draft.root ? optionalText(draft.nodeName) : undefined
  if (
    userId === false ||
    channelId === false ||
    username === false ||
    modelName === false ||
    useGroup === false ||
    tokenId === false ||
    nodeName === false
  ) {
    return undefined
  }
  return {
    filters: {
      start_timestamp: draft.start,
      end_timestamp: draft.end,
      ...(userId !== undefined ? { user_id: userId } : {}),
      ...(channelId !== undefined ? { channel_id: channelId } : {}),
      ...(username !== undefined ? { username } : {}),
      ...(modelName !== undefined ? { model_name: modelName } : {}),
      ...(useGroup !== undefined ? { use_group: useGroup } : {}),
      ...(tokenId !== undefined ? { token_id: tokenId } : {}),
      ...(nodeName !== undefined ? { node_name: nodeName } : {}),
    },
    options: {
      header: draft.header,
      csv_bom: draft.csvBom,
      timezone: draft.timezone,
    },
  }
}

/** Values shown for one quota job. Zero is a real filter and stays visible. */
export function quotaExportSummary(
  filters: QuotaExportJobFilters | undefined
): string {
  if (!filters) return ''
  const parts: string[] = []
  if (filters.username) parts.push(filters.username)
  else if (filters.user_id !== undefined) parts.push(String(filters.user_id))
  if (filters.model_name) parts.push(filters.model_name)
  if (filters.use_group) parts.push(filters.use_group)
  if (filters.channel_id !== undefined) parts.push(`#${filters.channel_id}`)
  if (filters.token_id !== undefined) parts.push(String(filters.token_id))
  if (filters.node_name) parts.push(filters.node_name)
  return parts.join(' · ')
}
