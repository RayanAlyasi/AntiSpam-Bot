-- +goose Up
-- The outbound action queue (DESIGN.md §10.1, §14): every WhatsApp write, paced per bot.
-- The table is the source of truth; executors keep only a window of it in memory.
CREATE TABLE app.actions (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    bot                 text        NOT NULL CHECK (bot <> ''),
    chat                text        NOT NULL CHECK (chat <> ''),
    kind                text        NOT NULL CHECK (kind IN ('revoke', 'kick', 'reject_join', 'send', 'edit', 'react',
                                                             'announce', 'join_approval', 'reset_invite')),
    priority            smallint    NOT NULL CHECK (priority BETWEEN 0 AND 4),
    payload             jsonb       NOT NULL DEFAULT '{}'::jsonb,
    status              text        NOT NULL DEFAULT 'pending'
                                    CHECK (status IN ('pending', 'running', 'done', 'failed', 'expired', 'cancelled', 'refused')),
    attempts            integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    not_before          timestamptz NOT NULL,
    deadline            timestamptz NOT NULL,
    message_time        timestamptz,
    -- Unique forever, so a retry, an edit or a second confirmation never repeats a write.
    dedupe_key          text        NOT NULL UNIQUE CHECK (dedupe_key <> ''),
    reason_detection_id bigint,
    last_error          text        NOT NULL DEFAULT '',
    result              jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- Whether a WhatsApp call was made for it (counts toward the daily writes alert).
    called              boolean     NOT NULL DEFAULT false,
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL
);

-- The executor's refill: pending work of one bot in priority, not_before, id order.
CREATE INDEX actions_pending_idx ON app.actions (bot, priority, not_before, id) WHERE status = 'pending';
-- Coalescing: pending kicks of one group.
CREATE INDEX actions_pending_chat_idx ON app.actions (bot, chat, kind, id) WHERE status = 'pending';
-- Crash recovery.
CREATE INDEX actions_running_idx ON app.actions (bot) WHERE status = 'running';
-- Daily write counts.
CREATE INDEX actions_called_idx ON app.actions (bot, updated_at) WHERE called;

-- Kill switch (scope 'global'), circuit breaker, ceiling override and slowed-down rate per bot.
CREATE TABLE app.action_controls (
    scope      text        PRIMARY KEY CHECK (scope <> ''),
    state      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS app.action_controls;
DROP TABLE IF EXISTS app.actions;
