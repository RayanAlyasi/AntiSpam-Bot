-- +goose Up
-- Admin reviews, their posted messages and votes (DESIGN.md §9.5, §14), the inbound-first DM
-- threads (§13.6) and evidence snapshots of deleted messages (§14, 30-day retention).

CREATE TABLE app.reviews (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    short_code      text        NOT NULL UNIQUE CHECK (short_code <> ''),
    group_jid       text        NOT NULL,
    group_name      text        NOT NULL DEFAULT '',
    kind            text        NOT NULL CHECK (kind IN ('ban_decision', 'uncertain', 'hacked', 'ask_before', 'network_incoming', 'appeal', 'alert')),
    detection_id    bigint,
    subject         jsonb       NOT NULL DEFAULT '{}'::jsonb,
    message_sender  text,
    message_id      text,
    evidence        jsonb       NOT NULL DEFAULT '{}'::jsonb, -- redacted text, links, reasons, score
    evidence_raw    text,       -- unredacted text, only until the decision (it feeds the corpus), then cleared
    thumbnail       bytea,
    thumbnail_mime  text,
    proposed        text        NOT NULL DEFAULT '',
    mode            text        NOT NULL DEFAULT '',
    status          text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'decided', 'timed_out', 'cancelled')),
    deadline        timestamptz NOT NULL,
    on_timeout      text        NOT NULL,
    decision        text,
    decided_by      text,
    decided_by_name text,
    method          text,
    decided_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX reviews_status_deadline_idx ON app.reviews (status, deadline);
CREATE INDEX reviews_group_pending_idx ON app.reviews (group_jid, created_at) WHERE status = 'pending';
CREATE INDEX reviews_message_idx ON app.reviews (group_jid, message_id) WHERE message_id IS NOT NULL;
CREATE INDEX reviews_created_idx ON app.reviews (created_at);

-- Maps the bot's outgoing review messages to their review, for reactions and replies.
CREATE TABLE app.review_messages (
    chat_jid       text        NOT NULL,
    msg_id         text        NOT NULL,
    review_id      bigint      NOT NULL REFERENCES app.reviews (id) ON DELETE CASCADE,
    sent_at        timestamptz NOT NULL,
    outcome_edited boolean     NOT NULL DEFAULT false,
    PRIMARY KEY (chat_jid, msg_id)
);

CREATE INDEX review_messages_review_idx ON app.review_messages (review_id);

-- Every vote, valid or not, for the audit trail.
CREATE TABLE app.review_votes (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    review_id      bigint      NOT NULL REFERENCES app.reviews (id) ON DELETE CASCADE,
    voter          text        NOT NULL,
    method         text        NOT NULL DEFAULT '',
    raw            text        NOT NULL DEFAULT '',
    decision       text        NOT NULL DEFAULT '',
    valid          boolean     NOT NULL,
    invalid_reason text        NOT NULL DEFAULT '',
    at             timestamptz NOT NULL
);

CREATE INDEX review_votes_review_idx ON app.review_votes (review_id, id);

-- A person's DM thread with the bot: open once they've written (inbound-first).
CREATE TABLE app.dm_threads (
    person            text PRIMARY KEY,
    last_inbound_at   timestamptz,
    last_exception_at timestamptz
);

-- Redacted evidence of deleted messages. Overturns clear the text and thumbnail.
CREATE TABLE app.evidence_snapshots (
    group_jid      text        NOT NULL,
    msg_id         text        NOT NULL,
    sender         text        NOT NULL DEFAULT '',
    text           text        NOT NULL DEFAULT '',
    thumbnail      bytea,
    thumbnail_mime text,
    detection_id   bigint,
    reason         text        NOT NULL DEFAULT '',
    at             timestamptz NOT NULL,
    expires_at     timestamptz,
    PRIMARY KEY (group_jid, msg_id)
);

CREATE INDEX evidence_snapshots_expires_idx ON app.evidence_snapshots (expires_at) WHERE expires_at IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS app.evidence_snapshots;
DROP TABLE IF EXISTS app.dm_threads;
DROP TABLE IF EXISTS app.review_votes;
DROP TABLE IF EXISTS app.review_messages;
DROP TABLE IF EXISTS app.reviews;
