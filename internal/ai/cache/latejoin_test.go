package cache

import (
	"context"
	"testing"
	"testing/synctest"
)

// A copy that arrives after every earlier caller gave up, while the cancelled call is still
// unwinding, starts its own call instead of inheriting the cancellation.
func TestSynctestLateCopyStartsAFreshCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _, _ := newCache(t, nil)
		unwind := make(chan struct{})
		first, cancel := context.WithCancel(t.Context())
		go func() {
			_, _, _ = c.Do(first, req("a", "cl"), func(ctx context.Context) (tv, error) {
				<-ctx.Done()
				<-unwind // still returning when the next copy arrives
				return tv{}, ctx.Err()
			})
		}()
		synctest.Wait()
		cancel()
		synctest.Wait()
		done := make(chan error, 1)
		go func() {
			v, _, err := c.Do(t.Context(), req("b", "cl"), func(context.Context) (tv, error) {
				return tv{Label: "spam", Conf: 0.95}, nil
			})
			if err == nil && v.Label != "spam" {
				t.Errorf("late copy got %+v", v)
			}
			done <- err
		}()
		synctest.Wait()
		close(unwind)
		if err := <-done; err != nil {
			t.Fatalf("late copy got %v, want its own fresh answer", err)
		}
	})
}
