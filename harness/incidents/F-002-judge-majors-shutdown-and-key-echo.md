# F-002: judge majors (round 1 FAIL): unbounded pool close and key echo

- Symptom 1: `pgxpool.Close` could block process exit without limit after a shutdown overrun.
- Symptom 2: `ai.api_key_env` could echo a pasted key. A user who puts the key itself in the field gets it printed in an error or log.
- Root cause: shutdown paths were written without a total deadline; config validation errors quoted the raw value of a secret-bearing field.
- Evidence: `.agent/logs/judge-F-002.md` (round 1 FAIL, then PASS on rounds 2 and 3); commit fbff1b4.
- Would have prevented: a rule that every shutdown step has a bounded wait, and that validation errors name the field but never print values of secret-adjacent fields.
