# Proposal: every stored field derived from message content is tested against the redactor

- Rule (for .claude/rules/tests.md): For each place that persists data derived from message content (URLs, normalized text, hashes, evidence), add a test that feeds a message containing a phone number, email and name and asserts none survive in any column, because redacting the main text field leaves the same data in side fields.
- Why: corpus URLs kept wa.me phone numbers although the text was redacted.
- Incident: harness/incidents/WP-G-judge-round1-majors.md
