//go:build integration

package pgstore_test

import (
	"context"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// TestOverturnOfMediaSpamAllowListsItsHash: an image-only spam that admins overturn gets its
// media hash allow-listed, not just a text SimHash (DESIGN §7.5 "SimHash/hash of the
// overturned message, URL or media").
func TestOverturnOfMediaSpamAllowListsItsHash(t *testing.T) {
	ctx := context.Background()
	pool := storetest.Pool(t)
	c := pgstore.Corpus{Pool: pool, HashKey: testKey, Persons: &people{}, Clock: clock.NewFake(start)}
	l := review.NewLearning(review.LearningDeps{Evidence: pgstore.Evidence{DB: pool}, Corpus: c, Standing: &reviewtest.Standing{}})
	msg := wa.MessageRef{Sender: spamLID, ID: "IMG1"}
	sha := make([]byte, 32)
	for i := range sha {
		sha[i] = byte(i + 1)
	}
	env := &envelope.Envelope{Group: group, MsgID: "IMG1", Kind: envelope.KindImage, Media: []envelope.MediaRef{{SHA256: sha}}}
	if err := l.ConfirmSpam(ctx, review.Confirmation{Group: group, Message: msg, Envelope: env, Source: "review"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Overturn(ctx, review.Overturn{Group: group, Message: msg}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM app.fingerprints WHERE kind = 'sha256' AND label = 'allow' AND scope_id = $1", string(group)).Scan(&n); err != nil || n != 1 {
		t.Errorf("media allow-list entries = %d, %v; want the image's hash allow-listed", n, err)
	}
}

func TestBlacklistListNeedsTheHashKey(t *testing.T) {
	l := pgstore.BlacklistList{DB: storetest.Pool(t)}
	if _, _, err := l.Add(context.Background(), blacklist.Entry{Scope: blacklist.ScopeGroup, Group: group, E164: "+966551234567"}); err == nil {
		t.Error("Add without a hash key succeeded, want it refused (content matching could never find the entry)")
	}
}
