package quark

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
)

var _ driver.Transfer = (*QuarkOrUC)(nil)
var _ driver.MaybeCannotTransfer = (*QuarkOrUC)(nil)

// UC shares use a separate provider; only advertise the Quark implementation.
func (d *QuarkOrUC) CanTransfer(string) bool { return d.config.Name == "Quark" }

func (d *QuarkOrUC) Transfer(ctx context.Context, dst model.Obj, shareURL, validCode string) error {
	if !d.CanTransfer("") {
		return errs.NotSupport
	}
	id, code, err := base.ParseShareLink(shareURL, validCode, "pan.quark.cn")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var token struct {
		Data struct {
			Token string `json:"stoken"`
		} `json:"data"`
	}
	err = d.shareRequest(ctx, "/share/sharepage/token", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{"pwd_id": id, "passcode": code})
	}, &token)
	if err != nil {
		return err
	}
	if token.Data.Token == "" {
		return fmt.Errorf("share token is empty")
	}
	var saved struct {
		Data struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	err = d.shareRequest(ctx, "/share/sharepage/save", http.MethodPost, func(req *resty.Request) {
		req.SetBody(base.Json{
			"pwd_id": id, "stoken": token.Data.Token,
			"pdir_fid": "0", "to_pdir_fid": dst.GetID(),
			"scene": "link", "pdir_save_all": true,
		})
	}, &saved)
	if err != nil {
		return err
	}
	if saved.Data.TaskID == "" {
		return fmt.Errorf("share save returned no task ID")
	}
	for attempt := 0; attempt < 60; attempt++ {
		var task struct {
			Data struct {
				Status *int `json:"status"`
			} `json:"data"`
		}
		err = d.shareRequest(ctx, "/task", http.MethodGet, func(req *resty.Request) {
			req.SetQueryParams(map[string]string{"task_id": saved.Data.TaskID, "retry_index": strconv.Itoa(attempt)})
		}, &task)
		if err != nil {
			return err
		}
		if task.Data.Status == nil {
			return fmt.Errorf("share task returned no status")
		}
		switch *task.Data.Status {
		case 2:
			return nil
		case 0, 1:
		default:
			return fmt.Errorf("share task failed with status %d", *task.Data.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("share transfer timed out")
}

func (d *QuarkOrUC) shareRequest(ctx context.Context, path, method string, callback base.ReqCallback, result any) error {
	raw, err := d.requestWithClient(base.RestyClient.Clone().SetRetryCount(0), path, method, func(req *resty.Request) {
		req.SetContext(ctx)
		callback(req)
	}, nil)
	if err != nil {
		return err
	}
	var status Resp
	if err := json.Unmarshal(raw, &status); err != nil {
		return fmt.Errorf("invalid share response: %w", err)
	}
	if status.Status != http.StatusOK || status.Code != 0 {
		return fmt.Errorf("share request failed (status %d, code %d): %s", status.Status, status.Code, status.Message)
	}
	return json.Unmarshal(raw, result)
}
