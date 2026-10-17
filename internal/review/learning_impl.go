package review

import (
	"context"
	"errors"
	"fmt"
)

func (l *Learning) snapshot(ctx context.Context, s Snapshot) error {
	if s.At.IsZero() {
		s.At = l.d.Clock.Now()
	}
	s.ExpiresAt = s.At.Add(EvidenceRetention)
	s.Text = redact(s.Text)
	if err := l.d.Evidence.Save(ctx, s); err != nil {
		return fmt.Errorf("save evidence for %s: %w", s.Message.ID, err)
	}
	return nil
}

func (l *Learning) confirmSpam(ctx context.Context, c Confirmation) error {
	if c.At.IsZero() {
		c.At = l.d.Clock.Now()
	}
	if _, err := l.d.Corpus.Confirm(ctx, c); err != nil {
		return fmt.Errorf("confirm spam %s: %w", c.Message.ID, err)
	}
	return nil
}

// overturn runs every step even when one fails, so a storage error in one place never leaves
// the overturned text alive in another.
func (l *Learning) overturn(ctx context.Context, o Overturn) error {
	var errs []error
	text := ""
	if snap, err := l.d.Evidence.Get(ctx, o.Group, o.Message.ID); err == nil {
		text = snap.Text
	} else if !errors.Is(err, ErrNotFound) {
		errs = append(errs, fmt.Errorf("evidence for %s: %w", o.Message.ID, err))
	}
	if err := l.d.Corpus.AllowFingerprint(ctx, FingerprintAllow{Group: o.Group, Message: o.Message, Text: text}); err != nil {
		errs = append(errs, fmt.Errorf("allow-list fingerprint of %s: %w", o.Message.ID, err))
	}
	// A message can be confirmed more than once (.spam, then a review ✅): retract every entry.
	for range 8 {
		id, ok, err := l.d.Corpus.EntryFor(ctx, o.Group, o.Message.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("corpus entry for %s: %w", o.Message.ID, err))
			break
		}
		if !ok {
			break
		}
		if err := l.d.Corpus.Retract(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("retract corpus entry %d: %w", id, err))
			break
		}
	}
	if err := l.d.Evidence.DeleteText(ctx, o.Group, o.Message.ID); err != nil && !errors.Is(err, ErrNotFound) {
		errs = append(errs, fmt.Errorf("delete evidence text of %s: %w", o.Message.ID, err))
	}
	if p := o.Message.Sender; p != "" && l.d.Standing != nil {
		errs = append(errs,
			l.d.Standing.RemoveStrike(ctx, o.Group, p, o.Message.ID),
			l.d.Standing.RestoreTrust(ctx, o.Group, p),
			l.d.Standing.RecordFalsePositive(ctx, o.Group, o.Message, o.Actor))
	}
	return errors.Join(errs...)
}
