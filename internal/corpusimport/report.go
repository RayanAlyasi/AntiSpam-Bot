package corpusimport

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// MinExamples is the fewest spam examples a category or language needs before the report stops
// calling it thin (§7.8 step 6).
const MinExamples = 10

// maxListedRejections is how many rejected lines Write prints before summing up the rest.
const maxListedRejections = 50

// Recall is how the pipeline handled the hold-out (§18): deleted, sent to admins or missed.
type Recall struct {
	Total, Deleted, ToAdmins, Missed int
}

// Report is what an import did (or, with DryRun, would do).
type Report struct {
	BatchID int64 // 0 for a dry run
	// Read counts the records read, rejected ones included; Spam and Ham split the valid ones by
	// label.
	Read, Spam, Ham int
	// Imported counts the corpus entries stored (or that would be).
	Imported int
	// MergedInFile counts spam records merged into an earlier near-identical record of the file;
	// MergedExisting those the live corpus already had.
	MergedInFile, MergedExisting int
	// Clusters counts the spam clusters (cluster_id or near-duplicate groups); Holdout the
	// records held out and HoldoutClusters their clusters.
	Clusters, Holdout, HoldoutClusters int
	Rejected                           []Rejection
	Recall                             Recall
	// HamCaught counts ham records the pipeline would have deleted or sent to admins.
	HamCaught int
	// Thin lists the categories and languages with fewer than MinExamples spam examples, such as
	// "category promo (3)".
	Thin   []string
	DryRun bool
}

// NewReport counts a plan: everything but the batch ID and the replay's results.
func NewReport(p Plan, rejected []Rejection, dryRun bool) Report {
	r := Report{
		// DropExisting takes the records the corpus already had off the corpus list.
		Spam: len(p.Corpus) + len(p.Holdout) + p.MergedInFile + p.MergedExisting, Ham: len(p.Ham),
		Imported: len(p.Corpus), MergedInFile: p.MergedInFile, MergedExisting: p.MergedExisting,
		Clusters: p.Clusters, Holdout: len(p.Holdout), HoldoutClusters: p.HoldoutClusters,
		Rejected: rejected, DryRun: dryRun,
	}
	r.Read = r.Spam + r.Ham + len(rejected)
	r.Recall.Total = len(p.Holdout)
	r.Thin = p.Thin
	return r
}

func thin(what string, counts map[string]int) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(counts)) {
		if counts[k] < MinExamples {
			out = append(out, fmt.Sprintf("%s %s (%d)", what, k, counts[k]))
		}
	}
	return out
}

// Write prints the report for the operator.
func (r Report) Write(w io.Writer) error {
	var b strings.Builder
	if r.DryRun {
		b.WriteString("dry run: nothing was written\n")
	} else {
		fmt.Fprintf(&b, "import batch %d (undo with: antispam corpus remove --import %d)\n", r.BatchID, r.BatchID)
	}
	fmt.Fprintf(&b, "records: %d read: %d spam, %d ham, %d rejected\n", r.Read, r.Spam, r.Ham, len(r.Rejected))
	verb := "imported"
	if r.DryRun {
		verb = "would import"
	}
	fmt.Fprintf(&b, "corpus: %s %d entries; duplicates merged: %d in the file, %d already in the corpus\n",
		verb, r.Imported, r.MergedInFile, r.MergedExisting)
	fmt.Fprintf(&b, "clusters: %d; hold-out: %d records in %d clusters\n", r.Clusters, r.Holdout, r.HoldoutClusters)
	if r.Recall.Total > 0 {
		fmt.Fprintf(&b, "hold-out recall: %d deleted, %d sent to admins, %d missed (of %d; %s caught)\n",
			r.Recall.Deleted, r.Recall.ToAdmins, r.Recall.Missed, r.Recall.Total, percent(r.Recall.Deleted+r.Recall.ToAdmins, r.Recall.Total))
	}
	if r.Ham > 0 {
		fmt.Fprintf(&b, "ham check: %d of %d ham records would have been deleted or sent to admins\n", r.HamCaught, r.Ham)
	}
	if len(r.Thin) > 0 {
		fmt.Fprintf(&b, "too few examples (under %d) to be useful: %s\n", MinExamples, strings.Join(r.Thin, ", "))
	}
	for i, rej := range r.Rejected {
		if i == maxListedRejections {
			fmt.Fprintf(&b, "... and %d more rejected lines\n", len(r.Rejected)-i)
			break
		}
		fmt.Fprintf(&b, "rejected %s\n", rej)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func percent(n, total int) string {
	return fmt.Sprintf("%.0f%%", 100*float64(n)/float64(cmp.Or(total, 1)))
}
