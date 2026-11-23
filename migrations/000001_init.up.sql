CREATE TABLE users (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    openid VARCHAR(128) NOT NULL UNIQUE,
    nickname VARCHAR(100),
    avatar_url TEXT,
    status VARCHAR(20) NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'disabled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE user_photos (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    storage_key TEXT NOT NULL,
    photo_type VARCHAR(30) NOT NULL DEFAULT 'portrait',
    consent_status VARCHAR(20) NOT NULL DEFAULT 'pending'
        CHECK (consent_status IN ('pending', 'granted', 'withdrawn')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX idx_user_photos_user_created ON user_photos(user_id, created_at DESC);

CREATE TABLE design_tasks (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    photo_id BIGINT NOT NULL REFERENCES user_photos(id),
    design_type VARCHAR(30) NOT NULL
        CHECK (design_type IN ('hairstyle', 'outfit', 'full_style')),
    style_code VARCHAR(80),
    status VARCHAR(20) NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'processing', 'succeeded', 'failed', 'cancelled')),
    provider VARCHAR(80),
    provider_task_id VARCHAR(255),
    error_code VARCHAR(80),
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);
CREATE INDEX idx_design_tasks_user_created ON design_tasks(user_id, created_at DESC);
CREATE INDEX idx_design_tasks_status_created ON design_tasks(status, created_at);

CREATE TABLE design_results (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id BIGINT NOT NULL REFERENCES design_tasks(id) ON DELETE CASCADE,
    result_type VARCHAR(30) NOT NULL DEFAULT 'image'
        CHECK (result_type IN ('image', 'suggestion')),
    storage_key TEXT,
    suggestion JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (storage_key IS NOT NULL OR suggestion IS NOT NULL)
);
CREATE INDEX idx_design_results_task ON design_results(task_id);

CREATE TABLE user_credits (
    user_id BIGINT PRIMARY KEY REFERENCES users(id),
    balance INT NOT NULL DEFAULT 0 CHECK (balance >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE credit_transactions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    task_id BIGINT REFERENCES design_tasks(id),
    amount INT NOT NULL CHECK (amount <> 0),
    transaction_type VARCHAR(30) NOT NULL
        CHECK (transaction_type IN ('grant', 'consume', 'refund', 'purchase', 'adjustment')),
    idempotency_key VARCHAR(128) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_credit_transactions_user_created ON credit_transactions(user_id, created_at DESC);
