//go:build integration

package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
)

// TestRawTextLeavesAfter72h: a review still pending after 72 h loses its unredacted text (but
// keeps the redacted evidence until the 30-day retention), however long its timeout (DESIGN §17).
func TestRawTextLeavesAfter72h(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	st, ev := pgstore.Reviews{DB: pool}, pgstore.Evidence{DB: pool}
	it, err := st.Create(ctx, review.Item{
		NewItem: item(), Code: "RAW2", Status: review.StatusPending,
		Deadline: start.Add(7 * 24 * time.Hour), OnTimeout: review.TimeoutKeepDeletedNoBan, CreatedAt: start,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := func() *string {
		var s *string
		if err := pool.QueryRow(ctx, "SELECT evidence_raw FROM app.reviews WHERE id = $1", it.ID).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if _, err := ev.Purge(ctx, start.Add(71*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if raw() == nil {
		t.Fatal("raw text gone before 72 h, want it kept for the decision")
	}
	if _, err := ev.Purge(ctx, start.Add(pgstore.RawTextRetention)); err != nil {
		t.Fatal(err)
	}
	if raw() != nil {
		t.Error("raw text still stored after 72 h, want it cleared (DESIGN §17)")
	}
	if got, _ := st.Get(ctx, it.ID); got.Evidence.Text == "" {
		t.Error("redacted evidence gone after 72 h, want it until the 30-day retention")
	}
}
