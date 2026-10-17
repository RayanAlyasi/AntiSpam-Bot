package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// ObserveSender records what a received message reveals about its sender (DESIGN.md §7.7): in a
// group, SenderAlt usually carries the other identifier (participant_pn), learned with source
// sender_alt; a DM to the bot links the sender's LID and phone number (source dm). A sender
// without an alternate address is still ensured, so every sender has a person.
func (s *Service) ObserveSender(ctx context.Context, info wa.MessageInfo, dm bool) (PersonID, error) {
	if info.IsFromMe {
		return 0, nil
	}
	pairSrc, aloneSrc := SourceSenderAlt, SourceEvent
	if dm {
		pairSrc, aloneSrc = SourceDM, SourceDM
	}
	sender, sk, ok := normalize(info.Sender)
	if !ok {
		return 0, nil // not a person (e.g. a channel); nothing to learn
	}
	if alt, ak, ok := normalize(info.SenderAlt); ok && ak != sk {
		lid, pn := sender, alt
		if sk == KindPN {
			lid, pn = alt, sender
		}
		p, err := s.Learn(ctx, lid, pn, pairSrc)
		if err != nil {
			return 0, fmt.Errorf("learn sender: %w", err)
		}
		// Learn returns the LID's person; a recycled number's sender is that person too.
		return p.ID, nil
	}
	p, err := s.Ensure(ctx, sender, aloneSrc)
	if err != nil && !errors.Is(err, ErrNotPerson) {
		return 0, fmt.Errorf("ensure sender: %w", err)
	}
	return p.ID, nil
}

// Fanout delivers identity events to several subscribers (back-fill, intelligence, the operator
// flag), in order. Every subscriber is called; their errors are joined.
type Fanout []Events

// Resolved implements Events.
func (f Fanout) Resolved(ctx context.Context, ev ResolvedEvent) error {
	var errs []error
	for _, e := range f {
		errs = append(errs, e.Resolved(ctx, ev))
	}
	return errors.Join(errs...)
}

// RecycledNumber implements Events.
func (f Fanout) RecycledNumber(ctx context.Context, ev RecycledEvent) error {
	var errs []error
	for _, e := range f {
		errs = append(errs, e.RecycledNumber(ctx, ev))
	}
	return errors.Join(errs...)
}
