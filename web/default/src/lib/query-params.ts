export interface UnixTimeRangeParams {
  start_time?: number
  end_time?: number
}

export function appendUnixTimeRangeParams(
  query: URLSearchParams,
  params?: UnixTimeRangeParams
) {
  if (params?.start_time !== undefined) {
    query.set('start_time', String(params.start_time))
  }
  if (params?.end_time !== undefined) {
    query.set('end_time', String(params.end_time))
  }
}
