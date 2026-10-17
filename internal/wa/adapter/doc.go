// Package adapter is the whatsmeow-backed side of internal/wa, and the only place that imports
// go.mau.fi/whatsmeow:
//
//   - Client implements wa.WAClient and maps whatsmeow errors to the wa error values;
//   - Unwrap removes message wrappers recursively (DESIGN.md §5.2);
//   - Build and BuildUndecryptable turn message events into Content Envelopes (§5.3), with
//     ParseVCard for contact cards.
//
// The connector (pairing, event handlers) is added by a later feature.
package adapter
