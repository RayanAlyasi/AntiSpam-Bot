// Package fake provides an in-memory wa.WAClient for tests: it records every call, serves the
// groups, users and channels a test sets up, and injects errors such as rate limits (429),
// temporary bans, "not admin" and timeouts. No test ever talks to real WhatsApp.
package fake
