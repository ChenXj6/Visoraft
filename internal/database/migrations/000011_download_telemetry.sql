ALTER TABLE task_steps
    ADD COLUMN detail jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE task_steps
    ADD CONSTRAINT task_steps_detail_object_check
    CHECK (jsonb_typeof(detail) = 'object');
