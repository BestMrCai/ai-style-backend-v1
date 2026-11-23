-- 为面部分析记录补充「本次分析」的性别与年龄段。
-- 关键点：性别/年龄段跟着照片走，不放在 users 上，
-- 因为同一个人可能帮别人分析不同的照片。
ALTER TABLE face_analyses
    ADD COLUMN gender VARCHAR(10)
        CHECK (gender IN ('male', 'female'));

ALTER TABLE face_analyses
    ADD COLUMN age_group VARCHAR(10)
        CHECK (age_group IN ('u18', '18_25', '26_35', '36_45', '46_55', '56p'));

CREATE INDEX idx_face_analyses_user_gender
    ON face_analyses(user_id, gender);

-- 设计任务保存本次生成用到的上下文快照，
-- 保证用户之后修改分析记录也不会让历史结果解释不通。
ALTER TABLE design_tasks
    ADD COLUMN gender VARCHAR(10);

ALTER TABLE design_tasks
    ADD COLUMN age_group VARCHAR(10);

ALTER TABLE design_tasks
    ADD COLUMN analysis_id BIGINT
        REFERENCES face_analyses(id) ON DELETE SET NULL;

-- 记录本次选用的是发型库里的哪一款，结果页据此显示「你选的是这款」。
ALTER TABLE design_tasks
    ADD COLUMN style_id VARCHAR(80);

CREATE INDEX idx_design_tasks_style
    ON design_tasks(style_id);

-- 新增设计模式 styleset：从推荐列表选择一款发型。
-- 与 reference（用户上传参考图）并列，两者都走同一张任务表。
ALTER TABLE design_tasks
    DROP CONSTRAINT IF EXISTS design_tasks_design_mode_check;

ALTER TABLE design_tasks
    ADD CONSTRAINT design_tasks_design_mode_check
    CHECK (design_mode IN ('auto', 'reference', 'styleset'));
