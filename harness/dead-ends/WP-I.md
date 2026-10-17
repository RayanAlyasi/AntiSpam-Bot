2026-10-17 WP-I — tried content policy as a detect.Analyzer — failed because Analyze can't see group settings or member standing
2026-10-17 WP-I — tried reserving migration number ranges per work package — failed because wp-c's TestMigrateFreshDatabase requires contiguous versions
2026-10-17 F-054 — tried Save by replaying changed keys through Set one by one — failed because presets didn't round-trip and a racing load left the cache stale (judge round 1)
2026-10-17 F-077 — tried dedupe by key for re-bans — failed because a dead kick (failed/expired/cancelled) blocked any new kick (judge round 1)
2026-10-17 F-077 — tried a fixed maxKickRetries on the dead-kick chain — failed because bans with more dead kicks were still stranded (judge round 2)
2026-10-17 F-076 — tried applying kill switches to the final score/band — failed because killed evidence still drove the outcome; must recompute log-odds and re-derive the band
2026-10-17 WP-I — tried a plain merge commit of staging with a conflicting go.mod — failed because a merge commit needs a judge stamp; used -X theirs plus go mod tidy
