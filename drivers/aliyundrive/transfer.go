package aliyundrive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
)

var _ driver.Transfer = (*AliDrive)(nil)

func (d *AliDrive) Transfer(ctx context.Context, dst model.Obj, shareURL, validCode string) error {
	id, code, err := base.ParseShareLink(shareURL, validCode, "alipan.com", "www.alipan.com", "aliyundrive.com", "www.aliyundrive.com")
	if err != nil {
		return err
	}
	if d.DriveId == "" || dst.GetID() == "" {
		return fmt.Errorf("destination drive or directory ID is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var token struct {
		Token string `json:"share_token"`
	}
	if err := d.shareRequest(ctx, "/v2/share_link/get_share_token", "", base.Json{"share_id": id, "share_pwd": code}, &token); err != nil {
		return err
	}
	if token.Token == "" {
		return fmt.Errorf("share token is empty")
	}
	// Fetch the complete root before starting any copies, including all pages.
	var files []File
	marker := ""
	seen := map[string]bool{}
	for {
		var page Files
		if err := d.shareRequest(ctx, "/adrive/v2/file/list_by_share", token.Token, base.Json{
			"share_id": id, "parent_file_id": "root", "limit": 100, "marker": marker,
			"order_by": "name", "order_direction": "ASC",
		}, &page); err != nil {
			return err
		}
		files = append(files, page.Items...)
		if page.NextMarker == "" {
			break
		}
		if seen[page.NextMarker] {
			return fmt.Errorf("share listing repeated a pagination marker")
		}
		seen[page.NextMarker] = true
		marker = page.NextMarker
	}
	if len(files) == 0 {
		return fmt.Errorf("share contains no files")
	}
	fileIDs := make(map[string]bool, len(files))
	for _, file := range files {
		if file.FileId == "" || fileIDs[file.FileId] {
			return fmt.Errorf("share contains an empty or repeated file ID")
		}
		fileIDs[file.FileId] = true
	}
	for _, file := range files {
		var batch struct {
			Responses []struct {
				Status int `json:"status"`
				Body   struct {
					RespErr
					FileID string `json:"file_id"`
					TaskID string `json:"async_task_id"`
				} `json:"body"`
			} `json:"responses"`
		}
		err := d.shareRequest(ctx, "/adrive/v4/batch", token.Token, base.Json{
			"resource": "file",
			"requests": []base.Json{{
				"id": file.FileId, "method": "POST", "url": "/file/copy",
				"headers": base.Json{"Content-Type": "application/json"},
				"body": base.Json{
					"share_id": id, "file_id": file.FileId,
					"to_drive_id": d.DriveId, "to_parent_file_id": dst.GetID(), "auto_rename": true,
				},
			}},
		}, &batch)
		if err != nil {
			return err
		}
		if len(batch.Responses) != 1 {
			return fmt.Errorf("share copy returned an invalid batch response")
		}
		res := batch.Responses[0]
		if res.Status < 200 || res.Status >= 300 || res.Body.Code != "" {
			return fmt.Errorf("share copy failed (status %d, code %s): %s", res.Status, res.Body.Code, res.Body.Message)
		}
		if res.Body.TaskID != "" {
			if err := d.waitShareTask(ctx, res.Body.TaskID); err != nil {
				return err
			}
		} else if res.Body.FileID == "" {
			return fmt.Errorf("share copy returned neither a file nor a task ID")
		}
	}
	return nil
}

func (d *AliDrive) shareRequest(ctx context.Context, path, token string, body base.Json, result any) error {
	raw, err, _ := d.requestWithClient(base.RestyClient.Clone().SetRetryCount(0), "https://api.alipan.com"+path, http.MethodPost, func(req *resty.Request) {
		req.SetContext(ctx).SetBody(body)
		if token != "" {
			req.SetHeader("X-Share-Token", token)
		}
	}, nil)
	if err != nil {
		return err
	}
	var status RespErr
	if err := json.Unmarshal(raw, &status); err != nil {
		return fmt.Errorf("invalid share response: %w", err)
	}
	if status.Code != "" {
		return fmt.Errorf("share request failed (%s): %s", status.Code, status.Message)
	}
	return json.Unmarshal(raw, result)
}

func (d *AliDrive) waitShareTask(ctx context.Context, taskID string) error {
	for attempt := 0; attempt < 60; attempt++ {
		var task struct {
			State  string `json:"state"`
			Status string `json:"status"`
		}
		if err := d.shareRequest(ctx, "/v2/async_task/get", "", base.Json{"async_task_id": taskID}, &task); err != nil {
			return err
		}
		if task.State == "" {
			task.State = task.Status
		}
		switch task.State {
		case "Succeed":
			return nil
		case "Running", "Pending":
		default:
			return fmt.Errorf("share copy task failed with state %q", task.State)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("share copy task timed out")
}
