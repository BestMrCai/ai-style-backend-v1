CREATE TABLE face_analyses (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    photo_id BIGINT NOT NULL REFERENCES user_photos(id),
    status VARCHAR(20) NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'processing', 'succeeded', 'failed')),
    result_json JSONB,
    overlay_key TEXT,
    error_code VARCHAR(80),
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);

CREATE INDEX idx_face_analyses_user_created
    ON face_analyses(user_id, created_at DESC);

CREATE INDEX idx_face_analyses_status_created
    ON face_analyses(status, created_at);
