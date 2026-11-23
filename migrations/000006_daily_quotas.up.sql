-- 每日功能次数限制。
--
-- 为什么单独建表而不用 COUNT(face_analyses)：
--   1) 用户可以删除记录，计数会跟着掉，等于把限额删回来了；
--   2) 一次 COUNT 扫全表，量上来以后每次提交都要算一遍。
--
-- 日期由服务端按东八区算好后传入，不用 CURRENT_DATE：
-- 数据库容器是 UTC，直接取会让"今天"在早上 8 点才切换。
CREATE TABLE daily_usage_quotas (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    quota_date DATE NOT NULL,
    feature VARCHAR(30) NOT NULL
        CHECK (feature IN ('face_analysis', 'hairstyle_design')),
    used INT NOT NULL DEFAULT 0 CHECK (used >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, quota_date, feature)
);

CREATE INDEX idx_daily_usage_quotas_date
    ON daily_usage_quotas(quota_date);
