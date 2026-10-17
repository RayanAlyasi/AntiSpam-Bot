-- +goose Up
-- One detection per message version and mode: a redelivered event (reconnect, retry) must not
-- record the same decision twice. edit_seq is 0 for the original message and counts edits, like
-- inbox.edit_seq (DESIGN.md §14); a network re-scan of the same message is its own mode.
ALTER TABLE app.detections ADD COLUMN edit_seq integer NOT NULL DEFAULT 0 CHECK (edit_seq >= 0);
DROP INDEX app.detections_group_msg_idx;
CREATE UNIQUE INDEX detections_message_idx ON app.detections (group_jid, msg_id, edit_seq, mode);

-- +goose Down
DROP INDEX IF EXISTS app.detections_message_idx;
CREATE INDEX detections_group_msg_idx ON app.detections (group_jid, msg_id);
ALTER TABLE app.detections DROP COLUMN IF EXISTS edit_seq;
