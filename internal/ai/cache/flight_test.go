package cache

import (
	"context"
	"testing"
)

func TestCopiesWaitingOnAFlightGetTheSanitizedVerdict(t *testing.T) {
	c, _, _ := newCache(t, func(o *Options[tv]) {
		o.Sanitize = func(v tv) tv { v.Reason = ""; return v }
	})
	started, release := make(chan struct{}), make(chan struct{})
	leader := func(context.Context) (tv, error) {
		close(started)
		<-release
		return tv{Label: "spam", Conf: 0.95, Reason: "group A member +971 50 123 4567"}, nil
	}
	type out struct {
		v   tv
		src Source
	}
	lead := make(chan out, 1)
	go func() {
		v, src, _ := c.Do(t.Context(), req("copy a", "cl"), leader)
		lead <- out{v, src}
	}()
	<-started
	follow := make(chan out, 1)
	go func() {
		v, src, _ := c.Do(t.Context(), req("copy b", "cl"), func(context.Context) (tv, error) {
			t.Error("a waiting copy made its own provider call")
			return tv{}, nil
		})
		follow <- out{v, src}
	}()
	// The follower either joins the flight or arrives after it; both must give the sanitized copy.
	close(release)
	l, f := <-lead, <-follow
	if l.v.Reason == "" || l.src != SourceFresh {
		t.Errorf("leader got %+v (%v), want its own unsanitized fresh verdict", l.v, l.src)
	}
	if f.v.Reason != "" || f.src != SourceCluster {
		t.Errorf("waiting copy got %+v (%v), want the sanitized cluster verdict (no other group's data, §17)", f.v, f.src)
	}
}

func TestLeaderCancellationDoesNotFailWaitingCopies(t *testing.T) {
	c, _, _ := newCache(t, nil)
	started, release := make(chan struct{}), make(chan struct{})
	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	go func() {
		_, _, _ = c.Do(leaderCtx, req("a", "cl"), func(ctx context.Context) (tv, error) {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return tv{}, ctx.Err()
			}
			return tv{Label: "spam", Conf: 0.95}, nil
		})
	}()
	<-started
	done := make(chan error, 1)
	go func() {
		_, _, err := c.Do(t.Context(), req("b", "cl"), func(context.Context) (tv, error) { return tv{Label: "spam", Conf: 0.9}, nil })
		done <- err
	}()
	cancelLeader()
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("waiting copy got %v after the first caller gave up, want the shared verdict", err)
	}
}
