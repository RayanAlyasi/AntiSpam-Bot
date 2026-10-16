---
paths:
  - "internal/actions/**"
---
# Action queue

- [Q-001] Exactly one executor per bot number, strictly serial. Pacing is the main protection against bans (DESIGN.md §10).
- [Q-002] The rate never rises above the configured ceiling on its own. Warning signs (429, unusual errors, slow acks, disconnects after writes) cut it sharply, and it recovers only up to the ceiling.
- [Q-003] The queue has no maximum length. Work is dropped only when it can no longer matter: deadline passed, message already gone, or superseded.
- [Q-004] Every action has a dedupe key and a deadline; deletes respect WhatsApp's ~2-day delete-for-everyone window.
- [Q-005] Kicks for the same group are coalesced into one call, at most `max_kick_batch` members per call.
