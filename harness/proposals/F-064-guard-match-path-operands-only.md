# Proposal: guard should match path operands, not the command string

Rule: The hidden-directory guard should block only commands whose path operands read or write the hidden directory (and allow `/dev/null` redirects), because matching the word anywhere blocks PR bodies, commit messages and judge probes that merely mention it.

File: the guard hook (.claude/ hooks; needs human approval) and the /next-feature instructions as an interim note.
Incident: harness/incidents/F-064-guard-blocks-hidden-dir-mention.md (recurrence of F-034-guard-blocks-hidden-dir-word.md)
