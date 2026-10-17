# F-064 follow-up merge: gate red from another package, nondeterministic by build

Symptom: `make check-fast` failed at the test step (gates.jsonl 19:57 and 19:59) on internal/wa/adapter TestChannelsInvitesPollsLocationsEventsCommerce/buttons: got [footer, Pick one, Claim], want [Pick one, footer, Claim]. The stamp hook refused to stamp while the gate was red, and the stop hook then forced a merge abort.

Root cause: wp-b's build_reflect.go:52 walks protobuf fields with `protoreflect.Message.Range`. protobuf-go swaps one adjacent pair when `detrand.Bool()` is true, and detrand hashes the running binary, so the order differs between `go test` (passes) and `go test -race` (fails 3/3). Pre-existing, not from the merge; also a latent production bug where joined text is hashed.

Evidence: .agent/logs/judge-F-064.md "Re-review: merge of origin/staging" (reproduced, root cause confirmed); .agent/logs/gates.jsonl; related note in F-064-loaded-machine-flaky-wallclock-tests.md, which saw the same test fail only with `-tags integration`.

Prevention: iterate `Descriptor().Fields()` by index with Has/Get (plus WhichOneof) wherever output order matters; route the fix to the wp-b owner. A red gate caused by an untouched package should be reported to its owner, not worked around.
