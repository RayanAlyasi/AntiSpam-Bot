package blacklist

import (
	"context"
	"errors"
	"fmt"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

func (r *Resolver) resolve(ctx context.Context, numbers []string) ([]Resolution, error) {
	batch := r.d.Batch
	if batch <= 0 {
		batch = DefaultBatch
	}
	out := make([]Resolution, 0, len(numbers))
	var errs []error
	for start := 0; start < len(numbers); start += batch {
		chunk := numbers[start:min(start+batch, len(numbers))]
		if r.d.Budget != nil {
			if err := r.d.Budget.Wait(ctx); err != nil {
				return out, errors.Join(append(errs, fmt.Errorf("read budget: %w", err))...)
			}
		}
		jids := make([]wa.JID, len(chunk))
		for i, n := range chunk {
			jids[i] = phoneJID(n)
		}
		infos, err := r.d.Users.UserInfo(ctx, jids)
		if err != nil {
			// Leave the rest unresolved; they still work as content signals.
			return out, errors.Join(append(errs, fmt.Errorf("user info for %d numbers: %w", len(chunk), err))...)
		}
		for i, n := range chunk {
			info, found := infos[jids[i]]
			res := Resolution{E164: n, Phone: jids[i], OnWhatsApp: found}
			if found && info.LID.IsLID() {
				res.LID = info.LID
				if r.d.Linker != nil {
					if err := r.d.Linker.Link(ctx, res.Phone, res.LID); err != nil {
						errs = append(errs, fmt.Errorf("link identity: %w", err))
					}
				}
			}
			if r.d.List != nil {
				if err := r.d.List.SetResolved(ctx, n, res.LID, res.OnWhatsApp); err != nil {
					errs = append(errs, fmt.Errorf("record resolution: %w", err))
				}
			}
			out = append(out, res)
		}
	}
	return out, errors.Join(errs...)
}
