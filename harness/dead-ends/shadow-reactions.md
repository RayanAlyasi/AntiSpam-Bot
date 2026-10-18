2026-10-18 shadow-reactions — tried gating reactions on verdict != clean — failed because still-uncertain messages have verdict clean but route ask, so the question-mark reaction would be hidden (caught by a unit test)
2026-10-18 shadow-reactions — tried gating on Plan.Mode == DetectionShadow alone — failed because safety rails can demote an enforce group to shadow, so reactions leaked into enforce groups (judge blocker)
2026-10-18 shadow-reactions — tried reactions on by default — failed because committed TestLaneShadowGroupRecordsAndNeverActs asserts shadow mode makes no WhatsApp calls; shipped off plus TCR
2026-10-18 shadow-reactions — tried git reset --hard to rebase work — failed because the guard blocks it; used git checkout -b from origin/staging
