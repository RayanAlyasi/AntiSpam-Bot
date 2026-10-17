# WP-K: parallel packages took colliding goose migration versions twice

Symptom: first a 00004 collision at merge, then a 00008 collision on staging after two PRs merged within minutes. Needed maintainer hotfix #34 and coordinator-allocated numbers.

Root cause: each package picked "next number" from its own branch base. A one-time renumber at merge doesn't help when other PRs land minutes later. Same cause as F-064; the earlier proposal was not adopted before WP-K ran.

Evidence: PR #34 and the staging history for the migrations directory; harness/incidents/F-064-migration-number-collision-after-staging-merge.md.

Prevention: the coordinator allocates numbers before packages start (see harness/proposals/F-106-migration-number-allocation.md), and the builder re-checks origin/staging immediately before the final push.
