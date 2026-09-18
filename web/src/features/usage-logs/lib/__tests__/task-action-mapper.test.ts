import { describe, expect, test } from 'vitest'

import {
  LEGACY_TASK_ACTION_ALIASES,
  TASK_ACTIONS,
} from '../../constants'
import { taskActionMapper } from '../mappers'

// 任务动作名在后端是 snake_case（constant/task.go），relay/relay_task.go 会用
// NormalizeTaskAction 把历史驼峰值一起改写成新值；前端的映射表跟着改，否则动作列与详情
// 弹窗里除 Suno 之外的每一条视频任务都显示 "Unknown"。

describe('task action mapper', () => {
  test.each([
    [TASK_ACTIONS.MUSIC, 'Generate Music', 'neutral'],
    [TASK_ACTIONS.LYRICS, 'Generate Lyrics', 'pink'],
    [TASK_ACTIONS.IMAGE_TO_VIDEO, 'Image to Video', 'blue'],
    [TASK_ACTIONS.TEXT_TO_VIDEO, 'Text to Video', 'blue'],
    [TASK_ACTIONS.FIRST_TAIL_TO_VIDEO, 'First/Last Frame to Video', 'blue'],
    [TASK_ACTIONS.REFERENCE_TO_VIDEO, 'Reference Video', 'blue'],
    [TASK_ACTIONS.REMIX, 'Video Remix', 'blue'],
  ])('labels the canonical action %s', (action, label, variant) => {
    expect(taskActionMapper.getLabel(action)).toBe(label)
    expect(taskActionMapper.getVariant(action)).toBe(variant)
  })

  test.each(Object.entries(LEGACY_TASK_ACTION_ALIASES))(
    'keeps the legacy action %s readable',
    (legacy, canonical) => {
      expect(taskActionMapper.getLabel(legacy)).toBe(
        taskActionMapper.getLabel(canonical)
      )
      expect(taskActionMapper.getLabel(legacy)).not.toBe('Unknown')
    }
  )

  test('falls back to Unknown for an action nobody knows', () => {
    expect(taskActionMapper.getLabel('not-a-task-action')).toBe('Unknown')
  })
})
