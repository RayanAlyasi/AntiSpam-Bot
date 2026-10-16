---
name: add-action
description: Adds a new kind of WhatsApp write action (send, revoke, kick, edit, lockdown, review message) to the paced action queue. Use whenever code needs to make WhatsApp do something.
---
Read `docs/DESIGN.md` §10 first. Every WhatsApp write goes through the queue; nothing calls whatsmeow write APIs directly.

1. Define the action kind with:
   - its priority (P0–P4, per §10.2);
   - a dedupe key;
   - a deadline after which it's pointless (deletes respect the ~2-day window);
   - whether it can be coalesced (kicks batch up to `max_kick_batch`).
2. Implement execution in `internal/actions/` through `WAClient` only. Classify errors as transient, rate-limited or permanent (§10.5).
3. Tests use the fake `WAClient` and `testing/synctest`:
   - pacing stays under the ceiling;
   - dedupe and cancellation work;
   - an action past its deadline is dropped;
   - the fake's 429 and temporary-ban errors slow the rate or open the breaker.
