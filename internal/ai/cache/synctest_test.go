package cache

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// Deterministic versions of the flight tests: synctest.Wait guarantees the second copy has
// joined the flight before the provider call returns.
func TestSynctestWaitingCopyGetsSanitizedVerdict(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _, _ := newCache(t, func(o *Options[tv]) { o.Sanitize = func(v tv) tv { v.Reason = ""; return v } })
		release := make(chan struct{})
		type res struct {
			v   tv
			src Source
		}
		leadCh, followCh := make(chan res, 1), make(chan res, 1)
		go func() {
			v, src, _ := c.Do(t.Context(), req("a", "cl"), func(context.Context) (tv, error) {
				<-release
				return tv{Label: "spam", Conf: 0.95, Reason: "group A details"}, nil
			})
			leadCh <- res{v, src}
		}()
		synctest.Wait()
		go func() {
			v, src, _ := c.Do(t.Context(), req("b", "cl"), func(context.Context) (tv, error) {
				t.Error("a copy waiting on the flight made its own call")
				return tv{}, nil
			})
			followCh <- res{v, src}
		}()
		synctest.Wait() // the copy is now blocked on the shared flight
		close(release)
		lead, follow := <-leadCh, <-followCh
		if lead.v.Reason == "" || follow.v.Reason != "" || follow.src != SourceCluster {
			t.Fatalf("leader %+v, copy %+v; want the raw verdict for the caller and the sanitized one for the copy", lead, follow)
		}
	})
}

func TestSynctestFlightOutlivesItsCallerButNotItsTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _, _ := newCache(t, func(o *Options[tv]) { o.FlightTimeout = time.Minute })
		leaderCtx, cancel := context.WithCancel(t.Context())
		flightErr := make(chan error, 1)
		go func() {
			_, _, _ = c.Do(leaderCtx, req("a", "cl"), func(ctx context.Context) (tv, error) {
				<-ctx.Done() // a hung dependency
				flightErr <- ctx.Err()
				return tv{}, ctx.Err()
			})
		}()
		synctest.Wait()
		copyErr := make(chan error, 1)
		go func() { _, _, err := c.Do(t.Context(), req("b", "cl"), nil); copyErr <- err }()
		synctest.Wait()
		cancel() // the first caller gives up; the flight keeps running for the copy
		synctest.Wait()
		select {
		case err := <-flightErr:
			t.Fatalf("flight stopped with its first caller: %v", err)
		default:
		}
		time.Sleep(time.Minute) // virtual time inside the bubble
		synctest.Wait()
		if len(flightErr) != 1 || len(copyErr) != 1 {
			t.Fatal("want the flight cut off at FlightTimeout and the waiting copy told")
		}
		if err := <-copyErr; err == nil {
			t.Fatal("waiting copy got no error from a flight that timed out")
		}
	})
}
