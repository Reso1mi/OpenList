package op

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

type transferReader struct {
	model.Storage
	dst    model.Obj
	config driver.Config
}

func (d *transferReader) Config() driver.Config                      { return d.config }
func (d *transferReader) GetAddition() driver.Additional             { return nil }
func (d *transferReader) Init(context.Context) error                 { return nil }
func (d *transferReader) Drop(context.Context) error                 { return nil }
func (d *transferReader) GetRoot(context.Context) (model.Obj, error) { return d.dst, nil }
func (d *transferReader) List(context.Context, model.Obj, model.ListArgs) ([]model.Obj, error) {
	return nil, nil
}
func (d *transferReader) Link(context.Context, model.Obj, model.LinkArgs) (*model.Link, error) {
	return nil, nil
}

type transferDriver struct {
	transferReader
	denied bool
	calls  int
	err    error
}

func (d *transferDriver) CanTransfer(path string) bool { return !d.denied }
func (d *transferDriver) Transfer(ctx context.Context, dst model.Obj, url, code string) error {
	d.calls++
	if dst != d.dst || url != "share" || code != "pwd" {
		panic("incorrect transfer arguments")
	}
	if _, ok := ctx.Deadline(); !ok {
		panic("missing transfer deadline")
	}
	return d.err
}

type transferResultDriver struct {
	transferReader
	calls int
}

func (d *transferResultDriver) Transfer(context.Context, model.Obj, string, string) ([]model.Obj, error) {
	d.calls++
	return []model.Obj{&model.Object{Name: "saved"}}, nil
}

func TestTransferShare(t *testing.T) {
	failure := errors.New("partial transfer failure")
	for _, tt := range []struct {
		name                 string
		config               driver.Config
		status               string
		denied               bool
		dir                  bool
		mask                 model.ObjMask
		cancel               bool
		driverErr, errorWant error
		calls                int
	}{
		{name: "success", dir: true, calls: 1},
		{name: "partial failure", dir: true, driverErr: failure, errorWant: failure, calls: 1},
		{name: "read only", dir: true, mask: model.NoWrite, errorWant: errs.PermissionDenied},
		{name: "file destination", errorWant: errs.NotFolder},
		{name: "runtime restriction", dir: true, denied: true, errorWant: errs.NotSupport},
		{name: "uninitialized", dir: true, config: driver.Config{CheckStatus: true}, status: "error", errorWant: errs.StorageNotInit},
		{name: "cancelled", dir: true, cancel: true, errorWant: context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			old := Cache
			Cache = NewCacheManager()
			t.Cleanup(func() { Cache = old })
			d := &transferDriver{transferReader: transferReader{Storage: model.Storage{MountPath: "/transfer-op", Status: tt.status}, dst: &model.Object{ID: "dst", IsFolder: tt.dir, Mask: tt.mask}, config: tt.config}, denied: tt.denied, err: tt.driverErr}
			key := Key(d, "/")
			Cache.dirCache.Set(key, newDirectoryCache([]model.Obj{&model.Object{Name: "subdir", IsFolder: true}}))
			Cache.dirCache.Set(Key(d, "/subdir"), newDirectoryCache(nil))
			Cache.detailCache.Set(d.MountPath, &model.StorageDetails{})
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), conf.SkipHookKey, struct{}{}))
			defer cancel()
			if tt.cancel {
				cancel()
			}
			err := TransferShare(ctx, d, "/", "share", "pwd")
			if !errors.Is(err, tt.errorWant) {
				t.Fatalf("got %v, want %v", err, tt.errorWant)
			}
			if d.calls != tt.calls {
				t.Fatalf("driver calls=%d want %d", d.calls, tt.calls)
			}
			if tt.calls > 0 {
				if _, ok := Cache.dirCache.Get(key); ok {
					t.Error("destination cache retained")
				}
				if _, ok := Cache.dirCache.Get(Key(d, "/subdir")); ok {
					t.Error("descendant cache retained")
				}
				if _, ok := Cache.GetStorageDetails(d); ok {
					t.Error("capacity cache retained")
				}
			}
		})
	}
	reader := transferReader{Storage: model.Storage{MountPath: "/transfer-result"}, dst: &model.Object{IsFolder: true}}
	if CanTransfer(&reader, "/") {
		t.Fatal("unsupported driver advertised transfer")
	}
	if err := TransferShare(context.Background(), &reader, "/", "share", "pwd"); !errors.Is(err, errs.NotSupport) {
		t.Fatal(err)
	}
	result := &transferResultDriver{transferReader: reader}
	ctx := context.WithValue(context.Background(), conf.SkipHookKey, struct{}{})
	if !CanTransfer(result, "/") {
		t.Fatal("result driver not supported")
	}
	if err := TransferShare(ctx, result, "/", "share", "pwd"); err != nil || result.calls != 1 {
		t.Fatalf("result transfer failed: %v", err)
	}
}
