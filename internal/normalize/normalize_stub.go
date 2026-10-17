package normalize

// Result is the normalized form of one text surface (DESIGN.md §7.2).
// Later normalization features add fields; callers read fields by name.
type Result struct {
	// Raw is the input, unchanged, kept for evidence.
	Raw string
	// Norm is the form used for matching.
	Norm string
	// Stripped counts the invisible and combining-flood characters removed as
	// obfuscation. It is a signal input.
	Stripped int
}

// Text normalizes one text surface.
func Text(raw string) Result {
	panic("not implemented")
}
