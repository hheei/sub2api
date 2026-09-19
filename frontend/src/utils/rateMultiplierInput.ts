/**
 * 倍率输入解析：管理员在同一个输入框里填数值或动态表达式（如 `$up * 1.05`）。
 *
 * 后端契约：提交时拆成 `rate_multiplier`（数值，表达式求值失败时的回退值）与
 * `rate_multiplier_expr`（表达式，空串表示静态倍率）。表达式保存前会以后端
 * `$up = 1` 试算校验，因此回退值仅在历史脏数据下生效。
 */

export interface ParsedRateMultiplierInput {
  rateMultiplier: number
  rateMultiplierExpr: string
}

/**
 * 分组默认倍率输入：数值须 > 0，其余非空输入按表达式处理（回退值 1）；
 * 空输入按静态 1 处理（分组默认倍率必填）。
 */
export function parseRateMultiplierInput(
  input: string | number | null | undefined
): ParsedRateMultiplierInput {
  const trimmed = String(input ?? '').trim()
  if (!trimmed) {
    return { rateMultiplier: 1.0, rateMultiplierExpr: '' }
  }
  const num = Number(trimmed)
  if (!isNaN(num) && Number.isFinite(num) && num > 0) {
    return { rateMultiplier: num, rateMultiplierExpr: '' }
  }
  return { rateMultiplier: 1.0, rateMultiplierExpr: trimmed }
}

/**
 * 用户专属倍率输入：0 是有效覆盖（不是"未设置"），空输入返回 null 表示清除覆盖。
 */
export function parseUserGroupRateInput(
  input: string | number | null | undefined
): ParsedRateMultiplierInput | null {
  const trimmed = String(input ?? '').trim()
  if (!trimmed) return null
  const num = Number(trimmed)
  // 非有限数字（含负数）交由后端校验报错，不静默降级成表达式。
  if (Number.isFinite(num)) {
    return { rateMultiplier: num, rateMultiplierExpr: '' }
  }
  return { rateMultiplier: 1.0, rateMultiplierExpr: trimmed }
}

/**
 * 回填输入框：优先展示原始表达式（管理员需看到自己写的表达式），其次数值。
 * 无覆盖返回空串。
 */
export function formatRateMultiplierInput(
  rate?: { rate_multiplier: number; rate_multiplier_expr?: string } | null
): string {
  if (!rate) return ''
  return rate.rate_multiplier_expr || String(rate.rate_multiplier)
}