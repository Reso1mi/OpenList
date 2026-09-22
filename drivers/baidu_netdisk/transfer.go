package baidu_netdisk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

var _ driver.Transfer = (*BaiduNetdisk)(nil)

func (d *BaiduNetdisk) Transfer(ctx context.Context, dst model.Obj, shareURL, validCode string) error {
	id, code, err := base.ParseShareLink(shareURL, validCode, "pan.baidu.com", "yun.baidu.com")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(id, "1") || len(id) == 1 {
		return fmt.Errorf("invalid Baidu share ID")
	}
	surl := id[1:]
	if !strings.HasPrefix(dst.GetPath(), "/") {
		return fmt.Errorf("destination path is not absolute")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var verified struct {
		Randsk string `json:"randsk"`
	}
	if err := d.shareRequest(ctx, http.MethodPost, map[string]string{"method": "verify", "surl": surl}, map[string]string{"pwd": code}, &verified); err != nil {
		return err
	}
	if verified.Randsk == "" {
		return fmt.Errorf("share verification returned an empty key")
	}
	sekey, err := url.QueryUnescape(verified.Randsk)
	if err != nil {
		return fmt.Errorf("invalid share verification key: %w", err)
	}
	const pageSize = 100
	var ids []int64
	var shareID, uk int64
	seen := map[int64]bool{}
	for page := 1; ; page++ {
		var listing struct {
			ShareID int64  `json:"share_id"`
			UK      int64  `json:"uk"`
			List    []File `json:"list"`
		}
		err := d.shareRequest(ctx, http.MethodGet, map[string]string{
			"method": "list", "shorturl": surl, "page": strconv.Itoa(page),
			"num": strconv.Itoa(pageSize), "root": "1", "fid": "0", "sekey": sekey,
		}, nil, &listing)
		if err != nil {
			return err
		}
		if listing.ShareID <= 0 || listing.UK <= 0 {
			return fmt.Errorf("share listing returned invalid owner or share ID")
		}
		if page == 1 {
			shareID, uk = listing.ShareID, listing.UK
		}
		if shareID != listing.ShareID || uk != listing.UK {
			return fmt.Errorf("share identity changed during listing")
		}
		for _, file := range listing.List {
			if file.FsId <= 0 || seen[file.FsId] {
				return fmt.Errorf("share listing returned an invalid or repeated file ID")
			}
			seen[file.FsId] = true
			ids = append(ids, file.FsId)
		}
		if len(listing.List) < pageSize {
			break
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("share contains no files")
	}
	// Small batches also work for accounts with lower transfer limits.
	for start := 0; start < len(ids); start += pageSize {
		batch, err := json.Marshal(ids[start:min(start+pageSize, len(ids))])
		if err != nil {
			return err
		}
		var saved struct {
			Extra struct {
				List []struct {
					Errno int `json:"errno"`
				} `json:"list"`
			} `json:"extra"`
		}
		err = d.shareRequest(ctx, http.MethodPost, map[string]string{
			"method": "transfer", "shareid": strconv.FormatInt(shareID, 10), "from": strconv.FormatInt(uk, 10),
		}, map[string]string{"sekey": sekey, "fsidlist": string(batch), "path": dst.GetPath(), "ondup": "newcopy"}, &saved)
		if err != nil {
			return err
		}
		for _, file := range saved.Extra.List {
			if file.Errno != 0 {
				return fmt.Errorf("share transfer entry failed (errno %d)", file.Errno)
			}
		}
	}
	return nil
}

// Unlike ordinary read requests, a transfer must not be automatically replayed
// after an ambiguous network failure: the previous request may have saved files.
func (d *BaiduNetdisk) shareRequest(ctx context.Context, method string, params, form map[string]string, result any) error {
	return d.shareRequestWithRefresh(ctx, method, params, form, result, true)
}

func (d *BaiduNetdisk) shareRequestWithRefresh(ctx context.Context, method string, params, form map[string]string, result any, allowRefresh bool) error {
	req := base.RestyClient.Clone().SetRetryCount(0).R().SetContext(ctx).SetQueryParam("access_token", d.AccessToken).SetQueryParams(params)
	if form != nil {
		req.SetFormData(form)
	}
	res, err := req.Execute(method, "https://pan.baidu.com/rest/2.0/xpan/share")
	if err != nil {
		return err
	}
	if res.IsError() {
		return fmt.Errorf("share request failed: HTTP %d", res.StatusCode())
	}
	var status struct {
		Errno  *int   `json:"errno"`
		Errmsg string `json:"errmsg"`
	}
	if err := json.Unmarshal(res.Body(), &status); err != nil {
		return fmt.Errorf("invalid share response: %w", err)
	}
	if status.Errno == nil {
		return fmt.Errorf("share response is missing errno")
	}
	// An explicit authentication rejection means the mutation was not accepted.
	// Refresh once, but never replay ambiguous transport or server errors.
	if allowRefresh && (*status.Errno == 111 || *status.Errno == -6) {
		if err := d.refreshTokenWithContext(ctx); err != nil {
			return err
		}
		return d.shareRequestWithRefresh(ctx, method, params, form, result, false)
	}
	if *status.Errno != 0 {
		return fmt.Errorf("share request failed (errno %d): %s", *status.Errno, status.Errmsg)
	}
	return json.Unmarshal(res.Body(), result)
}
