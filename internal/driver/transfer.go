package driver

import (
	"context"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

// Transfer saves all root entries of a provider share into an existing directory.
// Implement either Transfer or TransferResult. Both must honor cancellation and
// return an error if any entry fails, even if earlier entries were saved.
type Transfer interface {
	Transfer(ctx context.Context, dst model.Obj, shareURL, validCode string) error
}

// TransferResult is the alternative for drivers that can return saved objects.
type TransferResult interface {
	Transfer(ctx context.Context, dst model.Obj, shareURL, validCode string) ([]model.Obj, error)
}

type MaybeCannotTransfer interface {
	CanTransfer(path string) bool
}
