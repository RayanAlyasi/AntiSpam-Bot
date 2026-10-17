# F-064/F-065 (WP-H Postgres): guard blocked the builder from resolving a contract merge conflict

Symptom: after merging staging, .agent/contracts/F-064.md had conflict markers. The main agent's attempt was blocked (guard.jsonl 17:35:50Z: contract is protected). The architect then tried grep-style checks for conflict markers and was blocked twice (17:36:11Z, 17:36:17Z: "architect may only write ...; not >>>>>)'" and "not ]{7})'") because the guard read `>` inside a quoted regex as a redirect. Main also hit the hidden-test-directory block again (17:30:43Z, 17:36:47Z).

Root cause: the protection is correct, but there is no sanctioned path for merge conflicts in protected files, so the architect had to resolve it. Redirect detection matches `>` inside quoted regexes (same family as F-064-guard-blocks-hidden-dir-mention.md).

Evidence: .agent/logs/guard.jsonl entries 17:30:43Z to 17:36:47Z on feat/wp-h-postgres-wiring.

Prevention: document that conflicts in protected files are resolved by the architect and the builder stops and asks at once; make the guard ignore `>` inside quotes.
