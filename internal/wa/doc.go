// Package wa defines WAClient, the narrow interface through which everything talks to
// WhatsApp (DESIGN.md §2.3, §10, §18), plus the JID type and the error values callers
// classify. The whatsmeow-backed implementation lives in internal/wa/adapter and the
// recording fake in internal/wa/fake, so most code and tests never need a WhatsApp account.
package wa
