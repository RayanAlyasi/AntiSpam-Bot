-- +goose Up
-- Every non-clean verdict, and every decision with a route, with what was (or in shadow mode
-- would have been) done (DESIGN.md §7.3, §8.4, §14). No message text is stored here.
CREATE TABLE app.detections (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    group_jid         text             NOT NULL CHECK (group_jid <> ''),
    msg_id            text             NOT NULL CHECK (msg_id <> ''),
    person_id         bigint, -- app.persons(id); the foreign key is added once the persons table exists (wp-f)
    score             double precision NOT NULL CHECK (score >= 0 AND score <= 1),
    tier_reached      text             NOT NULL,
    signals           jsonb            NOT NULL DEFAULT '{}'::jsonb, -- {signals, violations, reason}
    verdict           text             NOT NULL CHECK (verdict IN ('spam', 'policy', 'clean')),
    confidence_source text             CHECK (confidence_source IN ('known', 'rules', 'ai')),
    route             text             NOT NULL CHECK (route IN ('auto_ban', 'ask', 'delete_only', 'allow', 'policy', 'none')),
    rule_version      text             NOT NULL,
    action_plan       jsonb            NOT NULL DEFAULT '[]'::jsonb,
    mode              text             NOT NULL CHECK (mode IN ('shadow', 'enforce', 'network_rescan')),
    network_ban_id    bigint,
    evidence_ref      text,
    created_at        timestamptz      NOT NULL DEFAULT now()
);

CREATE INDEX detections_group_created_idx ON app.detections (group_jid, created_at DESC);
CREATE INDEX detections_group_msg_idx ON app.detections (group_jid, msg_id);

-- +goose Down
DROP TABLE IF EXISTS app.detections;
