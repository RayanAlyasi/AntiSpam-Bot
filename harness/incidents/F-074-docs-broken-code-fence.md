# F-074: closing markdown fence had trailing text, so the rest of the doc rendered as code

Symptom: judge round 2 minor. docs/action-queue.md:83 ended an SQL block with a fence followed by prose on the same line, which does not close the fence; the following paragraphs and sections rendered as code and copied with the SQL.

Root cause: prose was appended on the same line as the closing fence. No lint covers markdown structure here.

Evidence: .agent/logs/judge-F-064.md WP-H wiring round 2; fixed and re-checked in round 3 PASS.

Prevention: a closing fence must be a bare fence line followed by a blank line.
