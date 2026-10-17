---
name: test-change-request
description: The legitimate way out when an acceptance test, a committed test or the contract looks wrong or contradicts docs/DESIGN.md. Files a request for a human and stops the feature. Use instead of editing, skipping or working around a test.
---
A test that looks wrong is either a real misunderstanding on your side or a real bug in the test. Either way a human decides; changing or dodging the test would hide the answer.

1. Write `harness/test-change-requests/<ID>-<short-slug>.md` with:
   - **Test:** the name and the failure output (from `.agent/logs/`).
   - **Why it looks wrong:** quote the DESIGN.md section or contract line it conflicts with.
   - **What you believe is correct,** and how confident you are.
   - **What you tried** before concluding this.
2. Add `BLOCKED: test-change-request <file>` at the top of `.agent/plan.md`.
3. Commit the request file alone (bookkeeping commits need no stamp), e.g. `chore: request change to <test name>`.
4. Stop and report the file path.
