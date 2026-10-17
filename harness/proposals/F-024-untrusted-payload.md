# Proposal: authority never comes from message payload

- **Rule (for `.claude/rules/whatsmeow.md`):** Treat every field of a received message (protobuf content, keys, client-set attributes) as attacker-controlled; decide revokes, admin status and edits only from what the server or our own state can vouch for, and document which.
- **Why:** a spammer controls the payload; trusting it lets them cancel actions or hide content.
- **From:** harness/incidents/F-024-payload-trust.md
