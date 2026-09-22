package op

import (
	"context"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/pkg/errors"
)

func CanTransfer(storage driver.Driver, path string) bool {
	_, plain := storage.(driver.Transfer)
	_, result := storage.(driver.TransferResult)
	if !plain && !result || storage.Config().CheckStatus && storage.GetStorage().Status != WORK {
		return false
	}
	if maybe, ok := storage.(driver.MaybeCannotTransfer); ok {
		return maybe.CanTransfer(path)
	}
	return true
}

func TransferShare(ctx context.Context, storage driver.Driver, dstPath, shareURL, validCode string) error {
	if storage.Config().CheckStatus && storage.GetStorage().Status != WORK {
		return errors.WithMessagef(errs.StorageNotInit, "storage status: %s", storage.GetStorage().Status)
	}
	if !CanTransfer(storage, dstPath) {
		return errors.WithStack(errs.NotSupport)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	// Require an existing destination: creating missing ancestors would require
	// checking their individual metadata permissions at the HTTP layer as well.
	dst, err := GetUnwrap(ctx, storage, dstPath)
	if err != nil {
		return errors.WithMessage(err, "failed to get destination directory")
	}
	if !dst.IsDir() {
		return errs.NotFolder
	}
	if model.ObjHasMask(dst, model.NoWrite) {
		return errs.PermissionDenied
	}
	switch d := storage.(type) {
	case driver.TransferResult:
		_, err = d.Transfer(ctx, dst, shareURL, validCode)
	case driver.Transfer:
		err = d.Transfer(ctx, dst, shareURL, validCode)
	}
	// A batch may have partially succeeded even when the provider returns an
	// error. Invalidate descendants too, since providers can merge directories.
	Cache.DeleteDirectoryTree(storage, dstPath)
	Cache.InvalidateStorageDetails(storage)
	if ctx.Value(conf.SkipHookKey) == nil && needHandleObjsUpdateHook() {
		go func() {
			hookCtx, hookCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
			defer hookCancel()
			objsUpdateHook(hookCtx, storage, dstPath, true)
		}()
	}
	return errors.WithMessage(err, "failed to transfer share")
}
