// Package logging sets up the bot's zerolog JSON logs (DESIGN.md §16): one writer, a level per
// module, request and detection IDs carried in the context, and masking for phone numbers,
// which are personal data and must never appear in full in a log (§17, rule GO-003).
//
// New builds a Loggers from the global level and the per-module overrides (parsed from
// LOG_LEVEL by the config package). Base returns the logger at the global level; For returns
// a logger with a "module" field at that module's level, so LOG_LEVEL=info,store=debug makes
// the store logger write debug lines while everything else stays at info.
//
// WithRequestID and WithDetectionID add a "request_id" or "detection_id" field to a logger and
// put both the tagged logger and the ID in the context. Downstream code logs through
// zerolog.Ctx(ctx) and reads the ID back with RequestID or DetectionID. NewID makes the IDs.
//
// Phone numbers and JIDs are logged through the Phone and JID string types, which mask
// themselves when formatted:
//
//	logger.Info().Stringer("phone", logging.Phone(number)).Stringer("jid", logging.JID(jid)).Msg("joined")
//
// They also implement encoding.TextMarshaler, so Interface and fmt verbs mask too. MaskPhone
// renders +966512345612 as "+966 5•• ••• •12": the country calling code, then the national
// number with only its first and last two digits visible. Input that isn't a recognisable
// international number becomes "•••" rather than being echoed. MaskJID masks the phone part of
// phone-number JIDs and leaves LIDs, groups and other opaque IDs readable.
package logging
