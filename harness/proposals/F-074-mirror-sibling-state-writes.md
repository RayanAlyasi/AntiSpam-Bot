# Proposal: copy the sibling path's full state transition

Rule: When a new code path clears or changes a field that other paths already change, mirror every field those paths write (for example Status together with BotIsAdmin) and assert the derived behaviour in the test, because markNotAdmin changed one field and the group stayed moderated.

File: .claude/rules/go.md
Incident: harness/incidents/F-074-marknotadmin-left-group-active.md
