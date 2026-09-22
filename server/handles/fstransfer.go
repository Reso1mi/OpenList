package handles

import (
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

type TransferReq struct {
	SrcURL    string `json:"url" form:"url" binding:"required,http_url"`
	DstDir    string `json:"dst_dir" form:"dst_dir" binding:"required"`
	ValidCode string `json:"valid_code" form:"valid_code"`
}

func FsTransfer(c *gin.Context) {
	var req TransferReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	dstPath, err := user.JoinPath(req.DstDir)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	meta, err := op.GetNearestMeta(dstPath)
	if err != nil && !errors.Is(err, errs.MetaNotFound) {
		common.ErrorResp(c, err, 500)
		return
	}
	if !common.CanWrite(user, meta, dstPath) ||
		(!user.CanWriteContent() && !common.CanWriteContentBypassUserPerms(meta, dstPath)) {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	if err := fs.Transfer(c.Request.Context(), dstPath, req.SrcURL, req.ValidCode); err != nil {
		code := 500
		if errors.Is(err, errs.PermissionDenied) {
			code = 403
		}
		common.ErrorResp(c, err, code)
		return
	}
	common.SuccessResp(c)
}
