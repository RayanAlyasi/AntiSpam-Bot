// Package envelope defines the Content Envelope (DESIGN.md §5.3): one normalized structure
// per message holding every text surface, URL, media reference, contact, channel and invite.
// Analyzers work on envelopes, never on raw protobufs; the proto-to-envelope builder lives
// under internal/wa/adapter, the only place allowed to import whatsmeow.
package envelope
