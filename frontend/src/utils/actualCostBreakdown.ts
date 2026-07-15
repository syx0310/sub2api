import type { ActualCostBreakdown } from '@/types'

const actualCostFields = [
  'input_actual_cost',
  'output_actual_cost',
  'cache_creation_actual_cost',
  'cache_read_actual_cost',
  'other_actual_cost'
] as const

function parseFiniteNumber(value: unknown): number | null {
  if (typeof value === 'number') {
    return Number.isFinite(value) ? value : null
  }
  if (typeof value !== 'string' || value.trim() === '') {
    return null
  }
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : null
}

export function parseActualCostBreakdown(value: unknown): ActualCostBreakdown | null {
  if (value === null || typeof value !== 'object') {
    return null
  }

  const source = value as Record<string, unknown>
  const parsed = {} as ActualCostBreakdown
  for (const field of actualCostFields) {
    if (!Object.prototype.hasOwnProperty.call(source, field)) {
      return null
    }
    const amount = parseFiniteNumber(source[field])
    if (amount === null) {
      return null
    }
    parsed[field] = amount
  }
  return parsed
}
