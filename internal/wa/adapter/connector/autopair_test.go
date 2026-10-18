package connector

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const pairNumber = "+966500000012" // made up; never paired

// scriptedPair is a fake PairFunc: each call shows a code and returns the next scripted result.
// A nil result pairs bot and adds it to the backend, as the whatsmeow store would.
type scriptedPair struct {
	backend *fakeBackend
	results []error
	calls   []time.Time
	phones  []string
}

func (p *scriptedPair) pair(_ context.Context, opts PairOptions) (wa.JID, error) {
	p.calls = append(p.calls, time.Now())
	p.phones = append(p.phones, opts.Phone)
	n := len(p.calls)
	opts.ShowCode("CODE-000" + string(rune('0'+n)))
	err := ErrPairTimeout
	if n <= len(p.results) {
		err = p.results[n-1]
	}
	if err == nil || errors.Is(err, errProfile) {
		p.backend.devices = append(p.backend.devices, Device{JID: bot})
		return bot, err
	}
	return "", err
}

// errProfile is a scripted result that links the device but fails the profile setup after it.
var errProfile = errors.New("set about text: boom")

// flakyDevices fails the first fails listings, then lists the backend's devices.
type flakyDevices struct {
	*fakeBackend
	fails int
}

func (f *flakyDevices) Devices(ctx context.Context) ([]Device, error) {
	if f.fails > 0 {
		f.fails--
		return nil, errors.New("store down")
	}
	return f.fakeBackend.Devices(ctx)
}

func TestAutoPair(t *testing.T) {
	paired := []Device{{JID: bot}}
	tests := []struct {
		name     string
		phone    string
		devices  []Device
		results  []error
		attempts int
		listErrs int // failed device listings before the first good one
		wantErr  bool
		// gaps are the waits between pairing calls, the first measured from the start.
		gaps      []time.Duration
		wantLog   []string
		rejectLog []string
	}{
		{name: "unset does nothing", devices: nil},
		{name: "already paired does nothing", phone: pairNumber, devices: paired},
		{name: "bad number is refused", phone: "+12", wantErr: true},
		{
			name: "already paired behind a store error does nothing", phone: pairNumber, devices: paired, listErrs: 2,
			wantLog: []string{"list paired devices"}, rejectLog: []string{"paired another way", "pairing code"},
		},
		{
			name: "store errors use up no codes", phone: pairNumber, listErrs: 3, attempts: 1, results: []error{nil},
			gaps: []time.Duration{30*time.Second + time.Minute + 2*time.Minute}, wantLog: []string{"CODE-0001", "paired by PAIR_PHONE"},
		},
		{
			name: "linked but profile failed stops", phone: pairNumber, results: []error{errProfile},
			gaps: []time.Duration{0}, wantLog: []string{"CODE-0001", "group-add privacy wasn't set"}, rejectLog: []string{"retrying", "paired another way"},
		},
		{
			name: "unpaired requests and logs a code, then stops on success", phone: pairNumber,
			results: []error{nil}, gaps: []time.Duration{0},
			wantLog: []string{"pairing code CODE-0001", "Linked devices → Link with phone number", "paired by PAIR_PHONE"},
		},
		{
			name: "expired codes are replaced at a growing pace", phone: pairNumber,
			results: []error{ErrPairTimeout, ErrPairTimeout, errors.New("network"), nil},
			gaps:    []time.Duration{0, 30 * time.Second, time.Minute, 2 * time.Minute},
			wantLog: []string{"CODE-0001", "expired unused", "CODE-0003", "pairing by PAIR_PHONE failed", "CODE-0004", "paired by PAIR_PHONE"},
		},
		{
			name: "gives up after the attempts", phone: pairNumber, attempts: 3,
			gaps:    []time.Duration{0, 30 * time.Second, time.Minute},
			wantLog: []string{"CODE-0003", "stopped requesting codes"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				backend := &fakeBackend{devices: tt.devices}
				p := &scriptedPair{backend: backend, results: tt.results}
				var logs bytes.Buffer
				start := time.Now()
				err := AutoPair(t.Context(), &flakyDevices{backend, tt.listErrs}, p.pair, AutoPairOptions{
					Phone: tt.phone, Attempts: tt.attempts, Log: zerolog.New(&logs),
				})
				if (err != nil) != tt.wantErr {
					t.Fatalf("AutoPair error = %v, want error %v", err, tt.wantErr)
				}
				if len(p.calls) != len(tt.gaps) {
					t.Fatalf("pairing calls = %d, want %d\n%s", len(p.calls), len(tt.gaps), logs.String())
				}
				prev := start
				for i, at := range p.calls {
					if got := at.Sub(prev); got != tt.gaps[i] {
						t.Errorf("call %d came %s after the previous one, want %s", i+1, got, tt.gaps[i])
					}
					prev = at
					if p.phones[i] != tt.phone {
						t.Errorf("call %d paired %q, want %q", i+1, p.phones[i], tt.phone)
					}
				}
				out := logs.String()
				if len(tt.wantLog) == 0 && out != "" {
					t.Errorf("logged %q, want nothing", out)
				}
				for _, reject := range tt.rejectLog {
					if strings.Contains(out, reject) {
						t.Errorf("log has %q:\n%s", reject, out)
					}
				}
				for _, want := range tt.wantLog {
					if !strings.Contains(out, want) {
						t.Errorf("log lacks %q:\n%s", want, out)
					}
				}
				if strings.Contains(out, strings.TrimPrefix(pairNumber, "+")) {
					t.Errorf("log shows the phone number unmasked:\n%s", out)
				}
				for line := range strings.SplitSeq(out, "\n") {
					if strings.Contains(line, "pairing code CODE-") && !strings.Contains(line, `"level":"info"`) {
						t.Errorf("code line is not at info level: %s", line)
					}
				}
				if len(backend.sessions) != 0 {
					t.Errorf("opened %d sessions; pairing must not connect a bot (nor send anything)", len(backend.sessions))
				}
				if tt.wantLog != nil && !strings.Contains(out, `"phone":"+966 5`) {
					t.Errorf("log lacks the masked phone:\n%s", out)
				}
			})
		})
	}
}

// TestAutoPairStopsWhenPairedElsewhere: a bot paired by `antispam pair` while a code waits
// ends the loop without another code.
func TestAutoPairStopsWhenPairedElsewhere(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		backend := &fakeBackend{}
		p := &scriptedPair{backend: backend}
		var logs bytes.Buffer
		done := make(chan error)
		go func() {
			done <- AutoPair(t.Context(), backend, func(ctx context.Context, opts PairOptions) (wa.JID, error) {
				_, err := p.pair(ctx, opts)
				backend.devices = []Device{{JID: bot}} // paired by the CLI meanwhile
				return "", err
			}, AutoPairOptions{Phone: pairNumber, Log: zerolog.New(&logs)})
		}()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if len(p.calls) != 1 {
			t.Errorf("pairing calls = %d, want 1", len(p.calls))
		}
		if !strings.Contains(logs.String(), "paired another way") {
			t.Errorf("log lacks the stop reason:\n%s", logs.String())
		}
	})
}

// TestAutoPairStopsWithContext: shutting down during a wait ends the loop at once.
func TestAutoPairStopsWithContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		backend := &fakeBackend{}
		p := &scriptedPair{backend: backend}
		done := make(chan error)
		go func() {
			done <- AutoPair(ctx, backend, p.pair, AutoPairOptions{Phone: pairNumber, Log: zerolog.Nop()})
		}()
		synctest.Wait() // first code shown, now waiting 30 s
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if len(p.calls) != 1 {
			t.Errorf("pairing calls = %d, want 1", len(p.calls))
		}
	})
}
