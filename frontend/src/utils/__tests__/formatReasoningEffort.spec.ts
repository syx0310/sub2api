import { describe, expect, it } from 'vitest'
import {
  formatReasoningEffort,
  formatReasoningEffortMapping,
  reasoningEffortValuesEqual
} from '@/utils/format'

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

describe('formatReasoningEffortMapping', () => {
  it('shows a single value when requested and forwarded match', () => {
    expect(formatReasoningEffortMapping('max', 'max')).toBe('Max')
    expect(formatReasoningEffortMapping(null, 'high')).toBe('High')
  })

  it('shows requested then forwarded when mapping changed the value', () => {
    expect(formatReasoningEffortMapping('max', 'xhigh')).toBe('Max → XHigh')
    expect(formatReasoningEffortMapping('high', 'medium')).toBe('High → Medium')
  })
})

describe('reasoningEffortValuesEqual', () => {
  it('treats x-high aliases as equal', () => {
    expect(reasoningEffortValuesEqual('x-high', 'xhigh')).toBe(true)
    expect(reasoningEffortValuesEqual('max', 'xhigh')).toBe(false)
  })
})
