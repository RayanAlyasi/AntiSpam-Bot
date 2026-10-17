// Package detect holds the analyzer pipeline (DESIGN.md §6, §7.1): the Analyzer interface,
// the registry and the tiers T0–T4. Analyzers read an envelope and emit weighted signals;
// they never touch storage or WhatsApp directly.
package detect
