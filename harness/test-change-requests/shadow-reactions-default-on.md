# Test-change request: let shadow mode react by default

**Status:** open. Not blocking: the feature ships with `shadow.reactions` off by default, and every
test passes.

## Test

`internal/app/fastlane` `TestLaneShadowGroupRecordsAndNeverActs`
(`fastlane_integration_test.go:49`), integration (`make check`). It passes today, with and
without this feature. Its last assertion pins the old meaning of shadow mode:

```go
if calls := client.Calls(); len(calls) != 0 {
	t.Fatalf("WhatsApp saw %d calls, want none: shadow mode acts on nothing (DESIGN §8.4)", len(calls))
}
```

The test never starts the executor (`lane.Run`), so it would keep passing even with the setting on
by default. That would only be because nothing runs the queue, which is a loophole, not a pass.

## Why it conflicts

The maintainer asked (2026-10-18, via the coordinator) for shadow mode to react to every message it
caught, ❌ when it would act without asking and ❓ when it would ask, so admins can see its judgement
live in the group. They wanted that on for v0.1.0 live testing. DESIGN.md §8.4 now says shadow
mode reacts when `shadow.reactions` is on. The test still says shadow mode makes no WhatsApp calls
at all. The coordinator's rule for this case: keep the default off, file this request, and let a
human decide.

## What I believe is correct (fairly confident)

Shadow mode posts reactions only, so the test should say exactly that:

1. **(Recommended)** Turn `shadow.reactions` on by default, and change the test to:
   - run the executor, or check the queue, and expect exactly one `react` action with emoji ❌ on
     `M1` in the protected group;
   - expect no revoke, kick or send, and nothing at all in the unprotected group.

   With the setting off, the test as it is still holds.

   The code change is two lines: `Shadow: Shadow{Reactions: true}` in `policy.Defaults()`, and
   `COALESCE((doc->'shadow'->>'reactions')::boolean, true)` in the `GroupPosting` query in
   `internal/store/queries/actions.sql`, then `make gen`. The hidden acceptance test
   `acceptance/policy` `TestShadowReactionsSetting` also pins the default to off and would need
   the same change.
2. Keep the default off. Admins who want the reactions send `.set shadow.reactions true` in each
   shadow group, and this request is closed.

## What I tried

Nothing to work around: the feature was built with the default off, as the coordinator's rule
says. The test was not edited.
