# Proposal: bounded shutdown and no secret values in errors

- Rule: Every shutdown step (pool close, River stop, server shutdown) must have a bounded wait inside one overall deadline, and config validation errors must name the field but never print the value of any key, token or secret-adjacent field, because an overrun can hang exit and a user may paste a real secret into an env-name field.
- Belongs in: Go rules file (lifecycle and config sections).
- Incident: `harness/incidents/F-002-judge-majors-shutdown-and-key-echo.md`
