package aistore

import (
	"testing"

	"github.com/riverqueue/river"
)

func TestRegisterPurgeAddsOnePeriodicJob(t *testing.T) {
	workers := river.NewWorkers()
	var periodic []*river.PeriodicJob
	if err := RegisterPurge(workers, &periodic, NewCache(nil), nil); err != nil {
		t.Fatal(err)
	}
	if len(periodic) != 1 {
		t.Fatalf("periodic jobs = %d, want the ai_cache purge", len(periodic))
	}
	if err := RegisterPurge(workers, &periodic, NewCache(nil), nil); err == nil {
		t.Fatal("registering the purge worker twice succeeded")
	}
}
