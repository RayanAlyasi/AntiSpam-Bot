# Test-change request: TestReplayInbox pins auto_ban for a group with no settings

**Status:** resolved. The maintainer chose option 1: the test now expects `route=ask` with the
default config (commit `0d90c45`, "test: expect new groups to ask the admins in the inbox
replay"), and replay mirrors the live bot's starting mode.

## Test

`cmd/antispam` `TestReplayInbox` (`replay_integration_test.go:118`), integration (`make check`):

```
--- FAIL: TestReplayInbox (0.17s)
    replay_integration_test.go:119: inbox replay output:
        120363000000000001@g.us I1 mode=enforce band=certain source=known route=ask actions=review
        120363000000000001@g.us I2 mode=enforce band=clean source=none route=none actions=-
        want I1 certain known and I2 clean
```

The test asserts `route=auto_ban` for I1. The group has no stored settings and the config is the
default one.

## Why it conflicts

Maintainer decision of 2026-10-18 (DESIGN.md §12.2 in this change): a group with no stored
settings starts in `enforcement.mode = ask_before_action` (config `new_groups.ask_admins`,
default `before`). The live bot routes certain known spam in that group to the admins (`ask`),
not `auto_ban`. The judge asked for `antispam replay -inbox` to use the same starting mode
(README step 7 recommends it before enforcing), so the replay now predicts `route=ask`. The test
pins what an unconfigured group did before the decision.

## Options

1. **(Recommended)** Change the test to say what it means. Either:
   - write a `config.yaml` with `new_groups: {ask_admins: off}` and pass it as `CONFIG_FILE` in
     `env`, keeping `route=auto_ban`; or
   - expect `route=ask` with the default config.
2. Keep the test as is, and leave `replay` predicting `Defaults()` (auto) for groups with no stored
   settings. That means reverting the `NewGroupMode` wiring in `cmd/antispam/replay.go` and
   `internal/app/fastlane/replay.go`. Replay would then disagree with the live bot for new groups
   until their settings are first saved.

Confidence that option 1 matches the decision: high.

## What I tried

Nothing that touches the test. `make check-fast` (unit and acceptance) is green. Only this
integration test fails.
