package quark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
)

func TestTransfer(t *testing.T) {
	for _, mode := range []string{"success", "token error", "empty token", "save error", "missing task", "task error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			old := base.RestyClient
			base.RestyClient = resty.New().SetRetryCount(3)
			t.Cleanup(func() { base.RestyClient = old })
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/share/sharepage/token":
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body["pwd_id"] != "abc123" || body["passcode"] != "1234" {
						t.Errorf("bad token body: %v", body)
					}
					if mode == "token error" {
						fmt.Fprint(w, `{"status":200,"code":41001,"message":"wrong code"}`)
						return
					}
					if mode == "empty token" {
						fmt.Fprint(w, `{"status":200,"code":0,"data":{}}`)
						return
					}
					fmt.Fprint(w, `{"status":200,"code":0,"data":{"stoken":"token"}}`)
				case "/share/sharepage/save":
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body["to_pdir_fid"] != "destination" || body["pdir_save_all"] != true || body["stoken"] != "token" {
						t.Errorf("bad save body: %v", body)
					}
					if mode == "save error" {
						fmt.Fprint(w, `{"status":200,"code":500,"message":"quota"}`)
						return
					}
					if mode == "missing task" {
						fmt.Fprint(w, `{"status":200,"code":0,"data":{}}`)
						return
					}
					fmt.Fprint(w, `{"status":200,"code":0,"data":{"task_id":"task"}}`)
				case "/task":
					if r.URL.Query().Get("task_id") != "task" {
						t.Error("missing task ID")
					}
					if mode == "task error" {
						fmt.Fprint(w, `{"status":200,"code":0,"data":{"status":3}}`)
						return
					}
					if mode == "cancel" {
						cancel()
						fmt.Fprint(w, `{"status":200,"code":0,"data":{"status":1}}`)
						return
					}
					fmt.Fprint(w, `{"status":200,"code":0,"data":{"status":2}}`)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer server.Close()
			d := &QuarkOrUC{config: driver.Config{Name: "Quark"}, conf: Conf{api: server.URL}}
			started := time.Now()
			err := d.Transfer(ctx, &model.Object{ID: "destination", IsFolder: true}, "https://pan.quark.cn/s/abc123?pwd=1234#/list/share", "")
			if (err == nil) != (mode == "success") {
				t.Fatalf("unexpected error: %v", err)
			}
			if mode == "cancel" && (!errors.Is(err, context.Canceled) || time.Since(started) > time.Second) {
				t.Fatalf("cancellation ignored: %v", err)
			}
			if mode == "success" && calls != 3 {
				t.Fatalf("got %d requests", calls)
			}
		})
	}
	d := &QuarkOrUC{config: driver.Config{Name: "UC"}}
	if d.CanTransfer("/") || !errors.Is(d.Transfer(context.Background(), nil, "", ""), errs.NotSupport) {
		t.Fatal("UC advertised Quark transfer")
	}
}
