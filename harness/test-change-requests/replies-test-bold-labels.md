# Test-change request: add replies_test.go to the reply-format approval

**Status:** blocks PR #52 (CI `tamper` fails on both runners).

## Test

`internal/api/chat/replies_test.go` (`TestRepliesWithoutWebsite`). I wrote it in #51. Since #51
merged into `staging` it counts as an existing test, so the tamper audit flags the lines PR #52
changes in it:

```
internal/api/chat/replies_test.go: line removed from an existing test:
  for i, want := range []string{"Protection is on in Riyadh Cars Market", "Three steps"} {
  {".settings", "review.destination: no review group (cases wait for .pending)"},
  {".status", "review.destination: no review group"},
```

(Reproduced with `git diff -U0 origin/staging -- '*_test.go'`. Locally `make check-fast` passes
because the local `staging` ref predates #51.)

## Why it needs changing

It's the same change the maintainer approved in
`harness/test-approvals/2026-10-18-reply-format-tests.md`. The maintainer asked for the group
name in "protection on" and the setting keys in the summaries to be bold, and this test pinned
the old unbolded text.

| Old expected substring | New expected substring |
|---|---|
| `Protection is on in Riyadh Cars Market` | `Protection is on in *Riyadh Cars Market*` |
| `review.destination: no review group (cases wait for .pending)` | `*review.destination:* no review group (cases wait for .pending)` |
| `review.destination: no review group` | `*review.destination:* no review group` |

What the test verifies doesn't change: no website, no empty link, and where the cases wait.

## What I believe is correct

Add `- internal/api/chat/replies_test.go` to the approval record (branch
`feat/clear-bot-replies`). Nothing else is needed: the branch already has the new expectations.
Confidence: high.

## What I tried

I checked whether the old substrings could still pass. They can't without undoing the bolding
the maintainer asked for. The other three test files are already approved.
