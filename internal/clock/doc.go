// Package clock provides the Clock interface, a real implementation backed by the time
// package and a manually advanced fake. Domain code takes a Clock instead of calling
// time.Now, so pacing, windows and deadlines are testable (with testing/synctest or Fake).
package clock
