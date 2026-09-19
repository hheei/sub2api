import { describe, expect, it } from 'vitest'

import {
  formatRateMultiplierInput,
  parseRateMultiplierInput,
  parseUserGroupRateInput
} from '../rateMultiplierInput'

describe('parseUserGroupRateInput', () => {
  it('treats a number as a static custom rate and clears the expression', () => {
    expect(parseUserGroupRateInput('0.8')).toEqual({ rateMultiplier: 0.8, rateMultiplierExpr: '' })
    // 数值输入必须清掉原有表达式，否则旧的动态表达式会继续生效。
    expect(parseUserGroupRateInput('1')).toEqual({ rateMultiplier: 1, rateMultiplierExpr: '' })
  })

  it('accepts 0 as a real override instead of treating it as unset', () => {
    expect(parseUserGroupRateInput('0')).toEqual({ rateMultiplier: 0, rateMultiplierExpr: '' })
    expect(parseUserGroupRateInput(0)).toEqual({ rateMultiplier: 0, rateMultiplierExpr: '' })
  })

  it('parses an expression with fallback 1 for a new override', () => {
    expect(parseUserGroupRateInput('$up * 1.05')).toEqual({
      rateMultiplier: 1,
      rateMultiplierExpr: '$up * 1.05'
    })
  })

  it('returns null for blank input, meaning the override is cleared', () => {
    expect(parseUserGroupRateInput('')).toBeNull()
    expect(parseUserGroupRateInput('   ')).toBeNull()
    expect(parseUserGroupRateInput(null)).toBeNull()
    expect(parseUserGroupRateInput(undefined)).toBeNull()
  })

  it('passes a negative number through as a numeric value so the backend rejects it', () => {
    // 静默降级成表达式会让 -1 变成"合法"表达式，隐藏配置错误。
    expect(parseUserGroupRateInput('-1')).toEqual({ rateMultiplier: -1, rateMultiplierExpr: '' })
  })
})

describe('parseRateMultiplierInput (group defaults)', () => {
  it('keeps positive numbers static and blank input at a static 1', () => {
    expect(parseRateMultiplierInput('2.5')).toEqual({ rateMultiplier: 2.5, rateMultiplierExpr: '' })
    expect(parseRateMultiplierInput('')).toEqual({ rateMultiplier: 1, rateMultiplierExpr: '' })
  })

  it('treats non-positive or non-numeric input as an expression', () => {
    expect(parseRateMultiplierInput('$up + 1')).toEqual({
      rateMultiplier: 1,
      rateMultiplierExpr: '$up + 1'
    })
    expect(parseRateMultiplierInput('0')).toEqual({ rateMultiplier: 1, rateMultiplierExpr: '0' })
  })
})

describe('formatRateMultiplierInput', () => {
  it('prefers the raw expression so admins see what they wrote', () => {
    expect(formatRateMultiplierInput({ rate_multiplier: 1, rate_multiplier_expr: '$up * 2' })).toBe(
      '$up * 2'
    )
    expect(formatRateMultiplierInput({ rate_multiplier: 0.75, rate_multiplier_expr: '' })).toBe('0.75')
    expect(formatRateMultiplierInput(null)).toBe('')
  })
})