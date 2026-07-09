import { describe, expect, it } from 'vitest'

import { formatReasoningEffort } from '@/utils/format'

describe('formatReasoningEffort', () => {
  it.each([
    ['low', 'Low'],
    ['medium', 'Medium'],
    ['high', 'High'],
    ['xhigh', 'XHigh'],
    ['x-high', 'XHigh'],
    ['max', 'Max'],
    ['ultra', 'Ultra']
  ])('formats %s as %s', (input, expected) => {
    expect(formatReasoningEffort(input)).toBe(expected)
  })

  it.each([null, undefined, '', 'none', 'minimal'])('formats %s as unavailable', (input) => {
    expect(formatReasoningEffort(input)).toBe('-')
  })
})
