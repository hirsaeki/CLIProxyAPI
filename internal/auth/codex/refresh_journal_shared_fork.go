package codex

import (
	"context"

	"github.com/MoeForge/moego/ojson"
)

// journalSharedRefreshReceipt makes a coalesced success durable in the caller's
// authoritative journal too. Keep exchange coalescing independent of journal
// identity: splitting it could send the same one-use grant to the provider twice.
// An existing pending claim is never overwritten by a follower.
func journalSharedRefreshReceipt[T any](ctx context.Context, journal RefreshJournal, clientID, grant string, value T, decode func(ojson.Result) (T, error)) (T, error) {
	receiptCtx, cancelReceipt := context.WithTimeout(context.WithoutCancel(ctx), codexRefreshTimeout)
	defer cancelReceipt()
	return journalRefresh(receiptCtx, journal, clientID, grant, func() (T, error) {
		return value, nil
	}, decode)
}
