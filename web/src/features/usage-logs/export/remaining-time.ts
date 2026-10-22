export type ExportProgressObservation = {
  startProgress: number
  startTime: number
  progress: number
  seconds: number | null
}

// Progress follows the scanned time range, not a row count. Only estimate from
// observations made while running; creation time would include queue delays.
export function observeExportProgress(
  previous: ExportProgressObservation | undefined,
  status: string,
  progress: number,
  observedAt: number
): ExportProgressObservation {
  const initial = {
    startProgress: progress,
    startTime: observedAt,
    progress,
    seconds: null,
  }
  if (
    status !== 'running' ||
    !Number.isFinite(progress) ||
    progress <= 0 ||
    progress >= 99 ||
    !previous ||
    progress < previous.progress ||
    observedAt <= previous.startTime
  ) {
    return initial
  }
  const advanced = progress - previous.startProgress
  if (advanced <= 0) return { ...previous, progress, seconds: null }
  const seconds =
    (((observedAt - previous.startTime) / 1000) * (100 - progress)) / advanced
  return {
    ...previous,
    progress,
    seconds:
      Number.isFinite(seconds) && seconds > 0 ? Math.ceil(seconds) : null,
  }
}
