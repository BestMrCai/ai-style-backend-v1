
-- 删除参考图片关联表。
DROP TABLE IF EXISTS design_task_photos;

-- 删除设计模式约束。
ALTER TABLE design_tasks
DROP CONSTRAINT IF EXISTS design_tasks_design_mode_check;

-- 删除新增字段。
ALTER TABLE design_tasks
DROP COLUMN IF EXISTS user_requirement;

ALTER TABLE design_tasks
DROP COLUMN IF EXISTS design_mode;
