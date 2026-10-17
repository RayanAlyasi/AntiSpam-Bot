// Package api serves the versioned JSON API under /api/v1 (DESIGN.md §13.1), described by an
// OpenAPI spec. It shares the settings keys, permission checks and audit log with the in-chat
// commands, and is what the separate frontend app builds on.
package api
