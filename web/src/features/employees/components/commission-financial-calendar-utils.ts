export function currentMonthValue() {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
}

export function shiftMonthValue(value: string, offset: number) {
  const [year, month] = value.split('-').map(Number)
  const date = new Date(
    year || new Date().getFullYear(),
    (month || 1) - 1 + offset,
    1
  )
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}`
}

export function monthValueToRange(value: string) {
  const [year, month] = value.split('-').map(Number)
  const start = new Date(
    year || new Date().getFullYear(),
    (month || 1) - 1,
    1,
    0,
    0,
    0
  ).getTime()
  const end = new Date(
    year || new Date().getFullYear(),
    month || 1,
    0,
    23,
    59,
    59
  ).getTime()
  return {
    start_time: Math.floor(start / 1000),
    end_time: Math.floor(end / 1000),
  }
}

export function monthValueToCalendarRange(value: string) {
  const currentMonth = currentMonthValue()
  if (value === currentMonth) {
    const now = Math.floor(Date.now() / 1000)
    return {
      start_time: now,
      end_time: now,
    }
  }

  const [year, month] = value.split('-').map(Number)
  const y = year || new Date().getFullYear()
  const m = (month || 1) - 1
  // 历史月份：start_time 只用于后端 ResolveCommissionMonthlyPeriod 识别周期，
  // end_time 只要大于任意月度周期结束时间即可，让后端以 period.PeriodEndAt 为自然上界，
  // 展示完整周期数据（前端不应截断历史数据）。
  const start = Math.floor(
    new Date(Date.UTC(y, m, 15, 12, 0, 0)).getTime() / 1000
  )
  return {
    start_time: start,
    end_time: start + 62 * 86400,
  }
}
