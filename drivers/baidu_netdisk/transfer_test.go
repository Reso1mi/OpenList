package baidu_netdisk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	for _, mode := range []string{"success", "wrong code", "listing error", "empty share", "transfer error", "entry error", "http error", "malformed response", "network error"} {
		t.Run(mode, func(t *testing.T) {
			old := base.RestyClient
			t.Cleanup(func() { base.RestyClient = old })
			lists, copies := 0, 0
			const firstID int64 = 9007199254740993
			base.RestyClient = resty.New().SetRetryCount(3).SetTransport(shareTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "pan.baidu.com" || r.URL.Path != "/rest/2.0/xpan/share" || r.URL.Query().Get("access_token") != "access" {
					t.Error("wrong endpoint or authorization")
				}
				_ = r.ParseForm()
				response := ""
				status := 200
				switch r.URL.Query().Get("method") {
				case "verify":
					if r.PostForm.Get("pwd") != "abcd" || r.URL.Query().Get("surl") != "shareID" {
						t.Error("incorrect code or share ID")
					}
					response = `{"errno":0,"randsk":"key%2Bwith%2Fescapes%3D"}`
					if mode == "wrong code" {
						response = `{"errno":-9}`
					}
				case "list":
					lists++
					if r.URL.Query().Get("sekey") != "key+with/escapes=" || r.URL.Query().Get("page") != fmt.Sprint(lists) {
						t.Error("incorrect key or page")
					}
					count := 100
					if lists == 2 {
						count = 1
					}
					if mode == "empty share" {
						count = 0
					}
					files := make([]File, count)
					for i := range files {
						files[i].FsId = firstID + int64((lists-1)*100+i)
					}
					raw, _ := json.Marshal(map[string]any{"errno": 0, "share_id": 123, "uk": 456, "list": files})
					response = string(raw)
					if mode == "listing error" {
						response = `{"errno":105}`
					}
				case "transfer":
					copies++
					if lists != 2 {
						t.Error("incomplete listing before mutation")
					}
					if r.PostForm.Get("path") != "/actual/root/destination" || r.PostForm.Get("sekey") != "key+with/escapes=" || r.URL.Query().Get("shareid") != "123" || r.URL.Query().Get("from") != "456" {
						t.Error("bad transfer destination or identity")
					}
					var ids []int64
					if err := json.Unmarshal([]byte(r.PostForm.Get("fsidlist")), &ids); err != nil {
						t.Fatal(err)
					}
					if ids[0] != firstID+int64((copies-1)*100) {
						t.Errorf("file ID lost precision: %v", ids)
					}
					response = `{"errno":0}`
					if mode == "transfer error" {
						response = `{"errno":-12}`
					}
					if mode == "entry error" {
						response = `{"errno":0,"extra":{"list":[{"errno":-30}]}}`
					}
					if mode == "http error" {
						status = 500
					}
					if mode == "malformed response" {
						response = `{}`
					}
					if mode == "network error" {
						return nil, errors.New("connection lost after save")
					}
				default:
					t.Error("unexpected method")
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response)), Request: r}, nil
			}))
			d := &BaiduNetdisk{Addition: Addition{AccessToken: "access"}}
			err := d.Transfer(context.Background(), &model.Object{Path: "/actual/root/destination", IsFolder: true}, "https://pan.baidu.com/s/1shareID?pwd=abcd", "")
			if (err == nil) != (mode == "success") {
				t.Fatalf("unexpected error %v", err)
			}
			if mode == "success" && copies != 2 {
				t.Fatalf("copied %d batches", copies)
			}
			if mode == "network error" && copies != 1 {
				t.Fatalf("mutation replayed %d times", copies)
			}
		})
	}
}

func TestTransferCancellation(t *testing.T) {
	old := base.RestyClient
	t.Cleanup(func() { base.RestyClient = old })
	base.RestyClient = resty.New().SetTransport(shareTransport(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := &BaiduNetdisk{}
	if err := d.Transfer(ctx, &model.Object{Path: "/destination"}, "https://pan.baidu.com/s/1abc", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not propagated: %v", err)
	}
}
