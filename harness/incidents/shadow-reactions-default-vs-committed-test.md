# shadow-reactions: default had to ship off

Symptom: the requested default (reactions on) conflicts with committed TestLaneShadowGroupRecordsAndNeverActs, which asserts shadow mode makes no WhatsApp calls.

Root cause: the request assumed a new default was free, but a committed test encodes "shadow never acts". Committed tests are not editable by agents.

Resolution: shipped default off and filed a TCR (/test-change-request). Commit 96cebc2 had requested "on by default" before this was found.

Evidence: commits 96cebc2, ccb5264; PR #66; TCR file.

Prevention: before choosing a default that changes shadow-mode behavior, run the existing lane tests for that mode and grep committed tests for "never acts"/no-call assertions.
