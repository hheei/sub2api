-- 批量生图任务冻结计费快照时使用的专属倍率是否为动态表达式。
-- 结算发生在提交之后的异步阶段，作业行是唯一的倍率来源；若不冻结该标记，
-- 结算写出的 usage_logs.is_dynamic_rate 只能靠可变的分组配置反推（不可靠）。
ALTER TABLE batch_image_jobs
    ADD COLUMN IF NOT EXISTS is_dynamic_rate BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN batch_image_jobs.is_dynamic_rate IS
    '提交时生效的计费倍率是否来自动态表达式（$up）；用于结算阶段如实标记 usage_logs.is_dynamic_rate。';
