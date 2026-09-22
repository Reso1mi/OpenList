package aliyundrive

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
)

type shareTransport func(*http.Request) (*http.Response, error)

func (f shareTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTransfer(t *testing.T) {
	for _, mode := range []string{"success", "token error", "empty token", "listing error", "repeated marker", "batch error", "missing response", "task failure", "network error"} {
		t.Run(mode, func(t *testing.T) {
			old := base.RestyClient
			t.Cleanup(func() { base.RestyClient = old; global.Delete("transfer-test") })
			global.Store("transfer-test", &State{})
			copies, lists := 0, 0
			base.RestyClient = resty.New().SetRetryCount(3).SetTransport(shareTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.alipan.com" {
					t.Errorf("unexpected host %s", r.URL.Host)
				}
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				response := ""
				switch r.URL.Path {
				case "/v2/share_link/get_share_token":
					if body["share_id"] != "abc123" || body["share_pwd"] != "explicit" {
						t.Errorf("bad token body %v", body)
					}
					response = `{"share_token":"token"}`
					if mode == "empty token" {
						response = `{}`
					}
					if mode == "token error" {
						response = `{"code":"InvalidPassword","message":"wrong code"}`
					}
				case "/adrive/v2/file/list_by_share":
					lists++
					if r.Header.Get("X-Share-Token") != "token" || body["parent_file_id"] != "root" {
						t.Error("missing share header or root")
					}
					if body["marker"] == "" {
						response = `{"items":[{"file_id":"file1"}],"next_marker":"next"}`
					} else {
						response = `{"items":[{"file_id":"file2"}]}`
					}
					if mode == "listing error" {
						response = `{"code":"ShareExpired"}`
					}
					if mode == "repeated marker" {
						response = `{"items":[],"next_marker":"next"}`
					}
				case "/adrive/v4/batch":
					copies++
					if lists != 2 {
						t.Error("copied before all pages were listed")
					}
					requests := body["requests"].([]any)
					copyBody := requests[0].(map[string]any)["body"].(map[string]any)
					if copyBody["to_drive_id"] != "drive" || copyBody["to_parent_file_id"] != "destination" || copyBody["file_id"] != []string{"file1", "file2"}[copies-1] {
						t.Errorf("bad destination/source %v", copyBody)
					}
					response = `{"responses":[{"status":201,"body":{"file_id":"saved"}}]}`
					if copies == 1 {
						response = `{"responses":[{"status":202,"body":{"async_task_id":"task"}}]}`
					}
					if mode == "batch error" {
						response = `{"responses":[{"status":403,"body":{"code":"QuotaExceeded"}}]}`
					}
					if mode == "missing response" {
						response = `{"responses":[]}`
					}
					if mode == "network error" {
						return nil, errors.New("connection lost after save")
					}
				case "/v2/async_task/get":
					if body["async_task_id"] != "task" {
						t.Error("bad task ID")
					}
					response = `{"state":"Succeed"}`
					if mode == "task failure" {
						response = `{"state":"PartialSucceed"}`
					}
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response)), Request: r}, nil
			}))
			d := &AliDrive{UserID: "transfer-test", DriveId: "drive"}
			err := d.Transfer(context.Background(), &model.Object{ID: "destination", IsFolder: true}, "https://www.aliyundrive.com/s/abc123?pwd=query", "explicit")
			if (err == nil) != (mode == "success") {
				t.Fatalf("unexpected error %v", err)
			}
			if mode == "success" && copies != 2 {
				t.Fatalf("copied %d files", copies)
			}
			if mode == "network error" && copies != 1 {
				t.Fatalf("mutation replayed %d times", copies)
			}
		})
	}
}

func TestTransferCancellation(t *testing.T) {
	old := base.RestyClient
	t.Cleanup(func() { base.RestyClient = old; global.Delete("transfer-cancel") })
	global.Store("transfer-cancel", &State{})
	base.RestyClient = resty.New().SetTransport(shareTransport(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := &AliDrive{UserID: "transfer-cancel", DriveId: "drive"}
	if err := d.Transfer(ctx, &model.Object{ID: "destination"}, "https://www.alipan.com/s/abc", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not propagated: %v", err)
	}
}
