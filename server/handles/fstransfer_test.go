package handles

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/gin-gonic/gin"
)

type transferHTTPDriver struct {
	model.Storage
	addition            driver.RootID
	calls               int
	denied              bool
	mask                model.ObjMask
	savedURL, savedCode string
}

func (d *transferHTTPDriver) Config() driver.Config {
	return driver.Config{Name: "TransferHTTPTest", NoCache: true}
}
func (d *transferHTTPDriver) GetAddition() driver.Additional { return &d.addition }
func (d *transferHTTPDriver) Init(context.Context) error     { return nil }
func (d *transferHTTPDriver) Drop(context.Context) error     { return nil }
func (d *transferHTTPDriver) GetRoot(context.Context) (model.Obj, error) {
	return &model.Object{ID: "root", IsFolder: true}, nil
}
func (d *transferHTTPDriver) Get(_ context.Context, path string) (model.Obj, error) {
	if path != "/destination" {
		return nil, errs.ObjectNotFound
	}
	return &model.Object{ID: "destination-id", Name: "destination", IsFolder: true, Mask: d.mask}, nil
}
func (d *transferHTTPDriver) List(context.Context, model.Obj, model.ListArgs) ([]model.Obj, error) {
	return []model.Obj{}, nil
}
func (d *transferHTTPDriver) Link(context.Context, model.Obj, model.LinkArgs) (*model.Link, error) {
	return nil, errs.NotSupport
}
func (d *transferHTTPDriver) CanTransfer(string) bool { return !d.denied }
func (d *transferHTTPDriver) Transfer(_ context.Context, dst model.Obj, url, code string) error {
	if dst.GetID() != "destination-id" {
		return fmt.Errorf("incorrect destination %s", dst.GetID())
	}
	d.calls++
	d.savedURL, d.savedCode = url, code
	return nil
}

func TestFsTransferPermissionsAndCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for i, tt := range []struct {
		name       string
		permission int32
		metaWrite  bool
		whitelist  []uint
		denied     bool
		mask       model.ObjMask
		wantCode   int
		capable    bool
	}{
		{name: "writer", permission: 1 << 3, wantCode: 200, capable: true},
		{name: "reader", wantCode: 403},
		{name: "meta grant", metaWrite: true, wantCode: 200, capable: true},
		{name: "meta denies writer", permission: 1 << 3, whitelist: []uint{999}, wantCode: 403},
		{name: "meta grant still checks whitelist", metaWrite: true, whitelist: []uint{999}, wantCode: 403},
		{name: "whitelisted writer", permission: 1 << 3, whitelist: []uint{42}, wantCode: 200, capable: true},
		{name: "driver restriction", permission: 1 << 3, denied: true, wantCode: 500},
		{name: "read only object", permission: 1 << 3, mask: model.NoWrite, wantCode: 403},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := &transferHTTPDriver{denied: tt.denied, mask: tt.mask}
			op.RegisterDriver(func() driver.Driver { return d })
			mount := fmt.Sprintf("/transfer-http-%d", i)
			id, err := op.CreateStorage(context.Background(), model.Storage{Driver: "TransferHTTPTest", MountPath: mount, Addition: `{}`})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = op.DeleteStorageById(context.Background(), id) })
			meta := &model.Meta{Path: mount + "/destination", Write: tt.metaWrite, WriteUsers: tt.whitelist}
			if err := op.CreateMeta(meta); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = op.DeleteMetaById(meta.ID) })
			user := &model.User{ID: 42, BasePath: mount, Permission: tt.permission}
			requestContext := context.WithValue(context.Background(), conf.UserKey, user)
			requestContext = context.WithValue(requestContext, conf.SkipHookKey, struct{}{})
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("POST", "/api/fs/transfer", strings.NewReader(`{"url":"https://pan.quark.cn/s/abc","dst_dir":"/destination","valid_code":"1234"}`)).WithContext(requestContext)
			c.Request.Header.Set("Content-Type", "application/json")
			FsTransfer(c)
			var response struct {
				Code int `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Code != tt.wantCode {
				t.Fatalf("got %s, want code %d", recorder.Body.String(), tt.wantCode)
			}
			if tt.wantCode == 200 {
				if d.calls != 1 || d.savedURL != "https://pan.quark.cn/s/abc" || d.savedCode != "1234" {
					t.Fatalf("incorrect dispatch: %+v", d)
				}
			} else if d.calls != 0 {
				t.Fatal("unauthorized transfer reached driver")
			}
			recorder = httptest.NewRecorder()
			c, _ = gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("POST", "/api/fs/list", nil).WithContext(requestContext)
			req := &ListReq{Path: "/destination"}
			req.Validate()
			FsList(c, req, user)
			var listing struct {
				Code int        `json:"code"`
				Data FsListResp `json:"data"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &listing); err != nil {
				t.Fatal(err)
			}
			if listing.Code != 200 || listing.Data.CanTransfer != tt.capable {
				t.Fatalf("capability mismatch: %s", recorder.Body.String())
			}
		})
	}
}

func TestFsTransferRejectsInvalidInput(t *testing.T) {
	for _, body := range []string{`{}`, `{"url":"not a URL","dst_dir":"/"}`, `{"url":"file:///etc/passwd","dst_dir":"/"}`} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("POST", "/api/fs/transfer", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		FsTransfer(c)
		var response struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Code != 400 {
			t.Fatalf("invalid request accepted: %s", recorder.Body.String())
		}
	}
}
