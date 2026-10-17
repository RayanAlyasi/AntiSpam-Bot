# F-024: builder trusted message payload for revokes

- **Symptom:** judge round 1 FAIL. A REVOKE ProtocolMessage beside spam dropped the spam and produced a revoke; ByAdmin was set whenever the key's participant differed from the sender (true for a PN key with a LID sender, and for a crafted revoke of someone else's message).
- **Root cause:** treated protobuf content and JID string equality as authority. The edit attribute is set by the sending client, the key is encrypted (unchecked by the server), and REVOKE is the enum's zero value.
- **Evidence:** .agent/logs/judge-F-024.md (round 1, majors 1–2); fix in 8c37be6 (`revoke()` in internal/wa/adapter/build.go).
- **Prevention:** treat every field of a received message as attacker-controlled; derive authority (revoke, admin, edit) only from attributes the policy can verify, and never from enum zero values or JID string comparisons across PN/LID forms.
