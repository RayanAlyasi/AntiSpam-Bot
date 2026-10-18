# Approve the shadow-react pacing test changes

**Date:** 2026-10-18 · **Approved by:** zxoir

branch: feat/shadow-react-pacing

- acceptance/actions/shadow_react_test.go
- acceptance/pipeline/shadow_react_test.go
- internal/actions/shadowreact_test.go
- internal/pipeline/shadowreact_test.go

**Why:** a shadow reaction now stands in for the delete it reports. It is paced like a delete (the per-group bucket of 10/min plus the bot's ceiling) instead of the 1-post-per-minute cap. It is queued at the priority of the action it stands in for (P1 for a delete, P3 when only a review would run) instead of P4 (DESIGN.md §8.4). These tests pinned the 1-minute gap and P4. Now they check the per-group bucket and the ceiling, still prove the 10-minute expiry with a bigger backlog, and check the would-be priority. What each test verifies is otherwise unchanged.
