package fs

import (
	"context"

	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/pkg/errors"
)

func Transfer(ctx context.Context, dstPath, shareURL, validCode string) error {
	storage, actualPath, err := op.GetStorageAndActualPath(dstPath)
	if err != nil {
		return errors.WithMessage(err, "failed to get destination storage")
	}
	return op.TransferShare(ctx, storage, actualPath, shareURL, validCode)
}
