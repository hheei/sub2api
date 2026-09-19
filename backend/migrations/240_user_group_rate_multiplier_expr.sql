-- 在"用户专属分组倍率表"上扩展动态倍率表达式列（对应 groups.rate_multiplier_expr）。
-- 语义：
--   - rate_multiplier      NULL 且 rate_multiplier_expr NULL → 该用户在此分组沿用分组默认倍率
--   - rate_multiplier      非 NULL                          → 静态专属倍率（0 亦为有效覆盖）
--   - rate_multiplier_expr 非空                             → 动态专属倍率（$up 为所选上游账号倍率），优先于静态值
ALTER TABLE user_group_rate_multipliers
    ADD COLUMN IF NOT EXISTS rate_multiplier_expr TEXT NULL;

COMMENT ON COLUMN user_group_rate_multipliers.rate_multiplier_expr IS
    '专属动态计费倍率表达式；非空时优先于 rate_multiplier。$up 为所选上游账号计费倍率。';
