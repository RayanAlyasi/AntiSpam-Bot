package neardup

// Stub for the Related closeness level (contract .agent/contracts/holdout-recall.md). The builder
// replaces it: Related must order between None and Loose, and Thresholds and Match gain the
// fields the contract names.

// Related is the weakest closeness: shared wording on match terms, below Loose.
const Related Closeness = 3

// CodeRelated is the signal code of a Related corpus match.
const CodeRelated = "corpus.related"

// WeightRelated is the default weight of CodeRelated.
const WeightRelated = 2.5

// Terms returns the distinct match terms of normalized text.
func Terms(norm string) []string { panic("not implemented") }
