package main

import (
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/alert"
)

func TestAlertSinks(t *testing.T) {
	tests := []struct {
		env  map[string]string
		want []string
	}{
		{map[string]string{}, nil},
		{map[string]string{"ALERT_WEBHOOK_URL": "https://h.example/x"}, []string{"webhook"}},
		{map[string]string{"ALERT_TELEGRAM_TOKEN": "t"}, nil}, // needs the chat too
		{map[string]string{"ALERT_TELEGRAM_TOKEN": "t", "ALERT_TELEGRAM_CHAT_ID": "1"}, []string{"telegram"}},
		{map[string]string{"ALERT_SMTP_ADDR": "smtp:25", "ALERT_EMAIL_TO": "a@x,b@x", "ALERT_SMTP_USER": "u"}, []string{"email"}},
	}
	for _, tt := range tests {
		sinks := alertSinks(func(k string) string { return tt.env[k] })
		var names []string
		for _, s := range sinks {
			names = append(names, s.Name())
		}
		if len(names) != len(tt.want) || (len(names) > 0 && names[0] != tt.want[0]) {
			t.Errorf("env %v: sinks %v, want %v", tt.env, names, tt.want)
		}
		if e, ok := firstEmail(sinks); ok && (len(e.To) != 2 || e.Auth == nil) {
			t.Errorf("email sink %+v: want two recipients and SMTP auth", e)
		}
	}
}

func firstEmail(sinks []alert.Sink) (*alert.Email, bool) {
	for _, s := range sinks {
		if e, ok := s.(*alert.Email); ok {
			return e, true
		}
	}
	return nil, false
}

func TestLeaseOwnerIsUniquePerCall(t *testing.T) {
	if a, b := leaseOwner(), leaseOwner(); a == b || a == "" {
		t.Errorf("leaseOwner = %q, %q; want unique IDs, so a restarted process never adopts an old lease", a, b)
	}
}

func TestPairCmdRejectsBadUsage(t *testing.T) {
	var out strings.Builder
	if code := pairCmd(t.Context(), []string{"extra"}, func(string) string { return "" }, &out); code != 2 {
		t.Errorf("pair with a stray argument: exit %d, want 2", code)
	}
	if !strings.Contains(out.String(), "usage: antispam pair") {
		t.Errorf("output %q: want the usage line", out.String())
	}
}
