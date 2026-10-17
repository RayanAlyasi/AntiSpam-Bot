// Package identity maps WhatsApp JIDs to persons (DESIGN.md §7.7): the LID is the primary
// identity, the phone number is added whenever it is learned, records are merged and
// back-filled on resolution, and recycled numbers never carry bans to a new LID.
package identity
