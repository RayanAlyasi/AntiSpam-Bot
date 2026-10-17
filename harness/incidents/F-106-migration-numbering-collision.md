# F-106: migration version numbering across parallel packages

- Symptom: 00090 broke TestMigrateFreshDatabase, which requires gap-free versions. 00004 then collided with #27. Final choice was 00007.
- Root cause: no rule or tool for allocating the next migration number when parallel sessions add migrations.
- Evidence: .agent/logs/check-test.log; #27 (0d726bb); commit 1ab1c28.
- Prevention: pick max(existing on staging and open PRs)+1, and re-check just before committing. Never leave gaps.
