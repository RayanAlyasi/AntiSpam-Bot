// Package actions is the outbound action queue and executor (DESIGN.md §10): the only code
// that performs WhatsApp writes. It is serial per bot number, with priorities, a fixed rate
// ceiling that only slows down, coalescing, deadlines, backoff and a circuit breaker.
package actions
