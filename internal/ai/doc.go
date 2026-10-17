// Package ai is the AI gateway (DESIGN.md §7.4): the provider-neutral AIClassifier interface,
// the verdict schema with strict validation and one retry, the versioned system prompt and the
// data-block renderer, and the configuration. Provider adapters (Anthropic, OpenAI-compatible,
// Google) build on this package; nothing outside them sees provider types. The AI is only
// called for the UNCERTAIN band, and its verdict is a signal: the decision engine acts.
package ai
