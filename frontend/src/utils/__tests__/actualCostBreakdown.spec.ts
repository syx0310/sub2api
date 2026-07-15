import { describe, expect, it } from 'vitest'

import { parseActualCostBreakdown } from '../actualCostBreakdown'

const completeBreakdown = {
  input_actual_cost: 12.3456,
  output_actual_cost: 4.5678,
  cache_creation_actual_cost: 1.2345,
  cache_read_actual_cost: 0.6789,
  other_actual_cost: 0.1234
}

describe('parseActualCostBreakdown', () => {
  it('returns all five finite numeric fields', () => {
    expect(parseActualCostBreakdown(completeBreakdown)).toEqual(completeBreakdown)
  })

  it('accepts non-empty finite numeric strings from compatible servers', () => {
    expect(parseActualCostBreakdown({
      ...completeBreakdown,
      input_actual_cost: '12.3456'
    })).toEqual(completeBreakdown)
  })

  it('treats missing or partial fields as unsupported', () => {
    const { other_actual_cost: _missing, ...partial } = completeBreakdown
    expect(parseActualCostBreakdown(partial)).toBeNull()
  })

  it.each([null, '', 'NaN', Number.NaN, Number.POSITIVE_INFINITY])(
    'rejects non-finite or empty values: %s',
    (invalid) => {
      expect(parseActualCostBreakdown({
        ...completeBreakdown,
        cache_read_actual_cost: invalid
      })).toBeNull()
    }
  )
})
