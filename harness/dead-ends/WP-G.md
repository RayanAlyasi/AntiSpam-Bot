2026-10-17 WP-G — tried building against uncommitted overlays of unmerged sibling packages — failed because they had to be removed and re-added for every merge and the staged tree never built alone
2026-10-17 WP-G — tried migration numbers 00030-00032 from the shared-notes range while wp-c's test required gap-free versions — failed because TestMigrateFreshDatabase broke; used 00004-00006 until #21 changed the test
2026-10-17 WP-G — tried a first-hit-wins precedence walk where weighted domain/URL/exact rules stop the walk — failed because they hid known evidence in wider scopes (judge round 1)
2026-10-17 WP-G — tried fuzzy-matching every phrase over the whole text — failed because a 60 KB message cost about 4 s; capped at 200 words with a length prefilter and ctx checks (judge round 1)
2026-10-17 WP-G — tried extracting phone numbers from raw text only — failed because circled, superscript, fullwidth and math digits evaded it; now also run on Norm (judge round 1)
2026-10-17 WP-G — tried matching the confusables skeleton on plain ASCII text — failed because Latin-to-Latin folds (m to rn) made "corn" match ".com"; gated on non-ASCII letters, still too coarse for mixed Arabic/English
2026-10-17 WP-G — tried opening F-102 as a separate PR from a second worktree — failed because the judge stamp is bound to the session's working directory
2026-10-17 WP-G — tried `golangci-lint fmt` over the packages touched — failed because it rewrote a sibling's committed test that the guard would not let be restored
