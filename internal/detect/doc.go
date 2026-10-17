// Package detect holds the analyzer framework (DESIGN.md §6, §7.1): the Analyzer interface, the
// registry and the tiered runner T0–T4. Analyzers read an envelope and emit weighted signals;
// they never touch storage or WhatsApp directly. Concrete analyzers live in their own packages.
//
// The framework is generic over the envelope type E and the group-policy type P, so it doesn't
// depend on how either is built. Production code instantiates it once, e.g.
//
//	type Analyzer = detect.Analyzer[envelope.Envelope, policy.Settings]
package detect
