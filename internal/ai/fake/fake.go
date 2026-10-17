// Package fake is a scripted ai.AIClassifier for tests: no network, no provider.
package fake

import (
	"context"
	"sync"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
)

// Response is one scripted answer.
type Response struct {
	Verdict ai.Verdict
	Err     error
	Block   bool // wait until the context ends, then return its error (a timeout)
}

// Classifier answers Classify calls from a script: queued responses first, in order, then the
// default. The zero default is an unsure verdict. It's safe for concurrent use.
type Classifier struct {
	mu       sync.Mutex
	caps     ai.Caps
	queue    []Response
	fallback Response
	requests []ai.ClassifyRequest
}

var _ ai.AIClassifier = (*Classifier)(nil)

// New returns a fake with the given capabilities that answers unsure until scripted.
func New(caps ai.Caps) *Classifier {
	return &Classifier{caps: caps, fallback: Response{Verdict: ai.Unsure("fake default")}}
}

// Push queues responses for the next calls.
func (c *Classifier) Push(rs ...Response) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queue = append(c.queue, rs...)
}

// SetDefault sets the response used once the queue is empty.
func (c *Classifier) SetDefault(r Response) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fallback = r
}

// Requests returns every request received so far, in order.
func (c *Classifier) Requests() []ai.ClassifyRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ai.ClassifyRequest(nil), c.requests...)
}

// Calls returns how many times Classify was called.
func (c *Classifier) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

// Capabilities returns the capabilities given to New.
func (c *Classifier) Capabilities() ai.Caps { return c.caps }

// Classify records the request and returns the next scripted response.
func (c *Classifier) Classify(ctx context.Context, req ai.ClassifyRequest) (ai.Verdict, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	r := c.fallback
	if len(c.queue) > 0 {
		r, c.queue = c.queue[0], c.queue[1:]
	}
	c.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return ai.Verdict{}, err
	}
	if r.Block {
		<-ctx.Done()
		return ai.Verdict{}, ctx.Err()
	}
	if r.Err != nil {
		return ai.Verdict{}, r.Err
	}
	return r.Verdict, nil
}
