//go:build integration

package pgstore_test

import (
	"context"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
)

func TestBlacklistBans(t *testing.T) {
	ctx := context.Background()
	m := pgstore.BlacklistBans{DB: storetest.Pool(t)}
	if _, ok, err := m.BanID(ctx, group, 7); ok || err != nil {
		t.Fatalf("BanID before Mark = %v, %v; want none", ok, err)
	}
	for _, ban := range []int64{11, 12} {
		if err := m.Mark(ctx, group, 7, ban); err != nil {
			t.Fatal(err)
		}
	}
	if id, ok, _ := m.BanID(ctx, group, 7); !ok || id != 12 {
		t.Fatalf("BanID after two marks = %d, %v; want the latest ban 12", id, ok)
	}
	if _, ok, _ := m.BanID(ctx, rgroup, 7); ok {
		t.Error("BanID in another group found a mark, want marks scoped to their group")
	}
	if err := m.Forget(ctx, group, 7); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := m.BanID(ctx, group, 7); ok {
		t.Error("BanID after Forget found a mark, want none")
	}
}
