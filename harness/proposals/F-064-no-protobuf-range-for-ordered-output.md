# Proposal: never use protobuf Range where order matters

Rule: Never derive ordered or hashed output from `protoreflect.Message.Range`; iterate `Descriptor().Fields()` by index, because protobuf-go randomises Range order per binary (detrand), so tests pass under `go test` and fail under `-race`.

File: .claude/rules/whatsmeow.md
Incident: harness/incidents/F-064-protobuf-range-order-made-gate-binary-dependent.md
