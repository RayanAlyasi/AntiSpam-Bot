package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestOpenRejectsMissingOrBadURL(t *testing.T) {
	tests := []struct {
		name, url string
		want      error
	}{
		{"empty", "", ErrNoDatabaseURL},
		{"garbage", "postgres://user:hunter2@[::1:5432/db", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := Open(context.Background(), tt.url)
			if err == nil {
				pool.Close()
				t.Fatal("Open succeeded, want an error (a bad DATABASE_URL must stop the bot)")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("got %v, want %v", err, tt.want)
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Errorf("error %q leaks the database password", err)
			}
		})
	}
}

func TestNewestMigrationCountsGoAndSQL(t *testing.T) {
	got, err := NewestMigration()
	if err != nil {
		t.Fatal(err)
	}
	if got < 3 {
		t.Errorf("newest migration %d, want >= 3 (schemas, River, audit_log)", got)
	}
}

func TestSchemaTooNewError(t *testing.T) {
	var err error = &SchemaTooNewError{DBVersion: 7, BinaryVersion: 5}
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Error("errors.Is(SchemaTooNewError, ErrSchemaTooNew) = false")
	}
	for _, want := range []string{"7", "5", "migration 7 or newer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q lacks %q (operators need to know which version to run)", err, want)
		}
	}
}

func TestWriteAuditValidatesBeforeTouchingTheDatabase(t *testing.T) {
	tests := []struct {
		name string
		e    AuditEntry
	}{
		{"no actor", AuditEntry{Action: "settings.update"}},
		{"no action", AuditEntry{Actor: ActorSystem}},
		{"diff is an array", AuditEntry{Actor: ActorSystem, Action: "x", Diff: []byte(`[1]`)}},
		{"diff is null", AuditEntry{Actor: ActorSystem, Action: "x", Diff: []byte(`null`)}},
		{"diff is not JSON", AuditEntry{Actor: ActorSystem, Action: "x", Diff: []byte(`{`)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A nil DBTX would panic if WriteAudit reached the database.
			if _, err := WriteAudit(context.Background(), nil, tt.e); !errors.Is(err, ErrInvalidAuditEntry) {
				t.Errorf("got %v, want ErrInvalidAuditEntry", err)
			}
		})
	}
}
