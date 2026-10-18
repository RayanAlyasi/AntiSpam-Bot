// Package pipelinetest builds the whole bot around a fresh Postgres (testcontainers) and the
// fake WAClient with a fake clock, so tests can deliver messages and observe what reaches
// WhatsApp (DESIGN.md §18). It needs Docker, so it only builds with the integration tag.
package pipelinetest
