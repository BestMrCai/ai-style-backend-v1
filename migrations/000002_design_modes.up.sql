
-- 1. 增加设计模式，旧任务默认使用 auto 模式。
ALTER TABLE design_tasks
ADD COLUMN design_mode VARCHAR(20) NOT NULL DEFAULT 'auto';

-- 2. 限制设计模式只能是 auto 或 reference。
ALTER TABLE design_tasks
ADD CONSTRAINT design_tasks_design_mode_check
CHECK (design_mode IN ('auto', 'reference'));

-- 3. 保存用户的文字设计要求。
ALTER TABLE design_tasks
ADD COLUMN user_requirement TEXT;

-- 4. 创建设计任务与参考图片的关联表。
CREATE TABLE design_task_photos (
    task_id BIGINT NOT NULL
        REFERENCES design_tasks(id) ON DELETE CASCADE,

    photo_id BIGINT NOT NULL
        REFERENCES user_photos(id),

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (task_id, photo_id)
);

-- 5. 建立照片反向查询索引。
CREATE INDEX idx_design_task_photos_photo
ON design_task_photos(photo_id);
