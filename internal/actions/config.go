package actions

import (
	"errors"
	"fmt"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/config"
)

// Config is the pacing and error handling of one bot's executor (DESIGN.md §10.3, §10.5).
// DefaultConfig holds the conservative defaults; the ceiling is only ever raised by an operator.
type Config struct {
	// CeilingPerMin is the sustained write rate the executor never exceeds on its own.
	CeilingPerMin int
	// Burst is how many writes may go out back to back at the start of a wave.
	Burst int
	// Spacing and Jitter: after every write the executor waits Spacing plus a uniform random
	// 0–Jitter, so the pattern doesn't look machine-regular.
	Spacing time.Duration
	Jitter  time.Duration

	// GroupPerMin caps writes to one chat in any 60 s, so a raided group can't starve others.
	GroupPerMin int
	// PostsPerGroupPerMin caps posts (send, edit, react) in a protected group whose silent mode
	// is off, in any 60 s. With silent on, posts there are refused, except shadow reactions.
	// Shadow reactions never count against it: they stand in for deletes and are paced like them.
	PostsPerGroupPerMin int
	// OTPPerMin and ReviewPerMin are reserved P3 sub-budgets: an OTP or review send may run ahead
	// of P1 and P2 work this often, so neither is starved by a raid. Zero disables a reserve.
	OTPPerMin    int
	ReviewPerMin int
	// ReadsPerMin and ReadBurst budget reads (IQs) separately; reads never use write budget.
	ReadsPerMin int
	ReadBurst   int

	// MaxKickBatch caps the participants per coalesced kick or join-request call.
	MaxKickBatch int
	// KickWindow is how long a kick or rejection waits for others in the same group to merge with.
	KickWindow time.Duration
	// UrgentWithin: within a priority, actions whose deadline is this close run first.
	UrgentWithin time.Duration

	// SlowdownFactor multiplies the rate on a warning sign (a quarter by default).
	SlowdownFactor float64
	// MinRatePerMin is the floor of a slow-down: a warning sign cuts the rate, but never below this.
	MinRatePerMin float64
	// RecoverEvery and RecoveryStep: after RecoverEvery of calm time the rate rises by
	// RecoveryStep writes/min, up to the ceiling.
	RecoverEvery time.Duration
	RecoveryStep float64
	// RateLimitPause is how long a 429 pauses the executor before it resumes, slowed down.
	RateLimitPause time.Duration
	// SlowAck: a call slower than this is a warning sign.
	SlowAck time.Duration
	// DisconnectWindow: a disconnect this soon after a write is a warning sign.
	DisconnectWindow time.Duration
	// CallTimeout bounds one WhatsApp call; a timeout is a transient error.
	CallTimeout time.Duration

	// MaxAttempts, BackoffBase and BackoffMax: a transient error is retried after a random
	// 0–min(2^n·BackoffBase, BackoffMax) (full jitter, n = failures so far minus one), at most
	// MaxAttempts calls in all.
	MaxAttempts int
	BackoffBase time.Duration
	BackoffMax  time.Duration

	// BreakerWarnings warning signs within BreakerWindow open the circuit breaker.
	BreakerWarnings int
	BreakerWindow   time.Duration
	// BreakerAllows are the kinds that still run at P0 while the breaker is open.
	BreakerAllows []Kind

	// DailyAlert: the operator is alerted once a day when the bot passes this many writes.
	// It doesn't slow anything down.
	DailyAlert int

	// PollInterval is how often the executor re-reads the table and the control state.
	PollInterval time.Duration
	// Window is how many pending actions per priority and chat the executor keeps in memory
	// (see Store.Candidates).
	Window int
}

// DefaultConfig returns the defaults of DESIGN.md §10.3–§10.5.
func DefaultConfig() Config {
	return Config{
		CeilingPerMin:       20,
		Burst:               10,
		Spacing:             time.Second,
		Jitter:              1500 * time.Millisecond,
		GroupPerMin:         10,
		PostsPerGroupPerMin: 1,
		OTPPerMin:           2,
		ReviewPerMin:        2,
		ReadsPerMin:         20,
		ReadBurst:           5,
		MaxKickBatch:        8,
		KickWindow:          2500 * time.Millisecond,
		UrgentWithin:        time.Hour,
		SlowdownFactor:      0.25,
		MinRatePerMin:       2,
		RecoverEvery:        15 * time.Minute,
		RecoveryStep:        1,
		RateLimitPause:      time.Minute,
		SlowAck:             10 * time.Second,
		DisconnectWindow:    time.Minute,
		CallTimeout:         time.Minute,
		MaxAttempts:         6,
		BackoffBase:         2 * time.Second,
		BackoffMax:          5 * time.Minute,
		BreakerWarnings:     3,
		BreakerWindow:       10 * time.Minute,
		BreakerAllows:       []Kind{KindAnnounce, KindJoinApproval, KindResetInvite},
		DailyAlert:          7500,
		PollInterval:        time.Second,
		Window:              50,
	}
}

// Validate reports the first setting that would make pacing unsafe or the executor stall.
func (c Config) Validate() error {
	var errs []error
	positive := func(name string, v int) {
		if v <= 0 {
			errs = append(errs, fmt.Errorf("%s must be positive, got %d", name, v))
		}
	}
	positiveDur := func(name string, v time.Duration) {
		if v <= 0 {
			errs = append(errs, fmt.Errorf("%s must be positive, got %s", name, v))
		}
	}
	positive("CeilingPerMin", c.CeilingPerMin)
	positive("Burst", c.Burst)
	positive("GroupPerMin", c.GroupPerMin)
	positive("PostsPerGroupPerMin", c.PostsPerGroupPerMin)
	positive("ReadsPerMin", c.ReadsPerMin)
	positive("ReadBurst", c.ReadBurst)
	positive("MaxKickBatch", c.MaxKickBatch)
	positive("MaxAttempts", c.MaxAttempts)
	positive("BreakerWarnings", c.BreakerWarnings)
	positive("Window", c.Window)
	positiveDur("RecoverEvery", c.RecoverEvery)
	positiveDur("CallTimeout", c.CallTimeout)
	positiveDur("BackoffBase", c.BackoffBase)
	positiveDur("BackoffMax", c.BackoffMax)
	positiveDur("BreakerWindow", c.BreakerWindow)
	positiveDur("PollInterval", c.PollInterval)
	if c.Spacing < 0 || c.Jitter < 0 || c.KickWindow < 0 || c.UrgentWithin < 0 || c.RateLimitPause < 0 {
		errs = append(errs, errors.New("spacing, jitter, kick window, urgent window and rate-limit pause can't be negative"))
	}
	if c.OTPPerMin < 0 || c.ReviewPerMin < 0 || c.DailyAlert < 0 {
		errs = append(errs, errors.New("OTP and review budgets and the daily alert can't be negative"))
	}
	if c.SlowdownFactor <= 0 || c.SlowdownFactor >= 1 {
		errs = append(errs, fmt.Errorf("SlowdownFactor must be in (0, 1): the queue only slows down, got %g", c.SlowdownFactor))
	}
	if c.RecoveryStep <= 0 {
		errs = append(errs, fmt.Errorf("RecoveryStep must be positive, got %g", c.RecoveryStep))
	}
	if c.MinRatePerMin <= 0 || c.MinRatePerMin > float64(c.CeilingPerMin) {
		errs = append(errs, fmt.Errorf("MinRatePerMin must be in (0, CeilingPerMin], got %g", c.MinRatePerMin))
	}
	for _, k := range c.BreakerAllows {
		if !k.Valid() {
			errs = append(errs, fmt.Errorf("BreakerAllows: unknown kind %q", k))
		}
	}
	return errors.Join(errs...)
}

// ConfigFromPacing builds the executor configuration from the instance's pacing settings
// (config.yaml "pacing", DESIGN.md §10.3); the settings it doesn't cover keep their defaults.
func ConfigFromPacing(p config.Pacing) Config {
	c := DefaultConfig()
	c.CeilingPerMin = p.CeilingPerMin
	c.Burst = p.Burst
	c.Spacing = p.SpacingBase
	c.Jitter = p.SpacingJitter
	c.GroupPerMin = p.GroupCeilingPerMin
	c.SlowdownFactor = p.SlowdownFactor
	c.MinRatePerMin = float64(p.MinRatePerMin)
	c.RecoveryStep = float64(p.RecoveryStepPerMin)
	c.RecoverEvery = p.RecoveryInterval
	c.MaxKickBatch = p.MaxKickBatch
	c.DailyAlert = p.DailyAlertWrites
	return c
}
