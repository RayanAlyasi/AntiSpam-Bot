package textmatch

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"unicode/utf8"
)

// Limits for admin-supplied regular expressions (DESIGN.md §17 "Regex DoS"). Go's RE2 runs in
// linear time, so the limits bound memory and per-message cost, not backtracking.
const (
	// MaxRegexLen is the longest accepted pattern, in runes.
	MaxRegexLen = 512
	// MaxRegexInsts is the largest accepted compiled program, in instructions. Counted
	// repetition expands the program, so `(a{100}){100}` is rejected here even though
	// RE2 itself would accept it.
	MaxRegexInsts = 5000
)

// Errors returned by CompileRegex; they are what an admin sees when a rule is rejected.
var (
	ErrRegexEmpty        = errors.New("regex is empty")
	ErrRegexTooLong      = fmt.Errorf("regex is longer than %d characters", MaxRegexLen)
	ErrRegexTooComplex   = errors.New("regex is too complex")
	ErrRegexMatchesEmpty = errors.New("regex matches empty text, so it would match every message")
	ErrRegexInvalid      = errors.New("regex is invalid")
)

// CompileRegex validates and compiles an admin-supplied pattern. It is called when a rule is
// saved, so an unacceptable pattern is rejected with a reason instead of being stored, and
// again when rules are loaded. Matching is case-insensitive, like keyword matching, and runs
// on prepared normalized text.
func CompileRegex(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, ErrRegexEmpty
	}
	if n := utf8.RuneCountInString(pattern); n > MaxRegexLen {
		return nil, fmt.Errorf("%w (%d)", ErrRegexTooLong, n)
	}
	re, err := syntax.Parse(pattern, syntax.Perl|syntax.FoldCase)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRegexInvalid, err)
	}
	prog, err := syntax.Compile(re.Simplify())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRegexInvalid, err)
	}
	if len(prog.Inst) > MaxRegexInsts {
		return nil, fmt.Errorf("%w (%d instructions, limit %d)", ErrRegexTooComplex, len(prog.Inst), MaxRegexInsts)
	}
	compiled, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRegexInvalid, err)
	}
	if compiled.MatchString("") {
		return nil, ErrRegexMatchesEmpty
	}
	return compiled, nil
}
