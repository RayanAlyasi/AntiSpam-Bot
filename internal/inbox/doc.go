// Package inbox is the durable buffer of raw WhatsApp events (DESIGN.md §4, §5.1).
// The connector inserts each event here before acking it, and analysis workers
// read from it, so events survive crashes and can be replayed for tests.
package inbox
