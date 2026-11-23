-- 使用记录：每一次面部分析 / AI 合成都留一条，免费也要留。
--
-- 为什么不复用 credit_transactions：
--   1) 免费的那次没有金额（amount <> 0 约束），根本进不去；
--   2) 账目表是给对账用的，不该混入"展示给用户看"的业务流水。
--
-- charge_type 只有两档，语义是"这一次是怎么付的"，不是支付渠道。
CREATE TABLE usage_records (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    feature VARCHAR(30) NOT NULL
        CHECK (feature IN ('face_analysis', 'hairstyle_design')),
    charge_type VARCHAR(10) NOT NULL
        CHECK (charge_type IN ('free', 'credit')),
    credits_cost INT NOT NULL DEFAULT 0 CHECK (credits_cost >= 0),
    -- 关联对象用于跳详情。用户删除任务时记录保留，只把关联置空：
    -- 消费历史是账，不该因为删了结果就消失。
    face_analysis_id BIGINT REFERENCES face_analyses(id) ON DELETE SET NULL,
    design_task_id BIGINT REFERENCES design_tasks(id) ON DELETE SET NULL,
    -- 业务时区的自然日，供"按日期查询"直接命中索引。
    quota_date DATE NOT NULL,
    -- 退回积分的时间。空表示没退过，也保证不会被重复退。
    refunded_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_usage_records_user_created
    ON usage_records(user_id, created_at DESC);

CREATE INDEX idx_usage_records_user_date
    ON usage_records(user_id, quota_date DESC);

-- 按任务反查用于失败退款：一条任务最多一条记录。
CREATE UNIQUE INDEX idx_usage_records_design_task
    ON usage_records(design_task_id)
    WHERE design_task_id IS NOT NULL;

CREATE UNIQUE INDEX idx_usage_records_face_analysis
    ON usage_records(face_analysis_id)
    WHERE face_analysis_id IS NOT NULL;
