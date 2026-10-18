# Approve the reply-format test changes

**Date:** 2026-10-18 · **Approved by:** maintainer

branch: feat/clear-bot-replies

- internal/review/service_test.go
- internal/api/chat/handler_test.go
- locales/locales_test.go
- internal/api/chat/replies_test.go
- cmd/antispam/replay_integration_test.go

**Why:** these tests pinned exact label punctuation ("Score: 0.97", "preset: standard", "Protection
is on in Riyadh Cars Market") and two message keys in locales/*.yaml, which blocked bold WhatsApp
labels and keeping one message catalogue. They now check the values themselves, and TestRender
renders from its own inline catalogue. What each test verifies is unchanged.
