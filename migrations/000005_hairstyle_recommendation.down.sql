ALTER TABLE design_tasks
    DROP CONSTRAINT IF EXISTS design_tasks_design_mode_check;

ALTER TABLE design_tasks
    ADD CONSTRAINT design_tasks_design_mode_check
    CHECK (design_mode IN ('auto', 'reference'));

DROP INDEX IF EXISTS idx_design_tasks_style;

ALTER TABLE design_tasks DROP COLUMN IF EXISTS style_id;
ALTER TABLE design_tasks DROP COLUMN IF EXISTS analysis_id;
ALTER TABLE design_tasks DROP COLUMN IF EXISTS age_group;
ALTER TABLE design_tasks DROP COLUMN IF EXISTS gender;

DROP INDEX IF EXISTS idx_face_analyses_user_gender;

ALTER TABLE face_analyses DROP COLUMN IF EXISTS age_group;
ALTER TABLE face_analyses DROP COLUMN IF EXISTS gender;
