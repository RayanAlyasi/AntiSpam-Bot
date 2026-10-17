# Proposal: enumerate every path before documenting "every X"

Rule: Before documenting or testing a hook or event as firing "for every X", grep for all code that produces X, including bulk SQL statements, and make the test count calls per ID against the store's final state, because the F-067 OnFinished hook silently missed rows finalised by a bulk UPDATE.

File: .claude/rules/queue.md
Incident: harness/incidents/F-067-onfinished-missed-bulk-failpending.md
