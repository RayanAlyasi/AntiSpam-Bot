# Proposal: River context and synctest bubbles

- Rule: Start River with `context.WithoutCancel(ctx)` and stop it explicitly, and create every channel or timer a `synctest` test uses inside the bubble, because a cancelled start context sends River's notifier into a retry loop and out-of-bubble channels hang the test.
- Belongs in: Go rules file, or the River and testing notes.
- Incident: `harness/incidents/F-002-river-cancelled-ctx-and-synctest.md`
