# 分享文件转存接口

参考 [OpenListTeam/OpenList#1962](https://github.com/OpenListTeam/OpenList/pull/1962) 的接口设计，支持将分享根目录中的全部文件和文件夹保存到同一网盘的已挂载目录。

支持的目标驱动：

| 驱动 | 分享链接 |
| --- | --- |
| `Quark`（Cookie） | `https://pan.quark.cn/s/<id>` |
| `Aliyundrive` | `https://www.alipan.com/s/<id>`、`https://www.aliyundrive.com/s/<id>` |
| `BaiduNetdisk` | `https://pan.baidu.com/s/1<id>`、`https://yun.baidu.com/s/1<id>` |

`QuarkOpen`、`QuarkTV`、`UC`、`AliyundriveOpen` 和分享只读驱动不提供此能力。阿里云盘这里使用现有 `Aliyundrive` 驱动的账号认证；Open 驱动的令牌不能直接用于这些接口。

## 请求

`POST /api/fs/transfer`，使用现有 API 登录认证：

```json
{
  "url": "https://pan.quark.cn/s/example",
  "dst_dir": "/夸克/收藏",
  "valid_code": "1234"
}
```

- `url`：必填，分享链接。URL 只用于提取分享 ID，服务端请求固定的网盘 API 地址。
- `dst_dir`：必填，相对于当前用户基础路径的 OpenList 目录。目录必须已存在，且挂载的驱动须与分享来源匹配。
- `valid_code`：可选，提取码。留空时读取链接中的 `pwd` 查询参数。
- 转存分享根目录的全部内容；URL 片段中的子目录选择不会改变转存范围。

成功沿用现有响应格式：`{"code":200,"message":"success","data":null}`。失败返回非 200 的响应体 `code` 和错误信息。

`POST /api/fs/list` 的响应增加 `data.can_transfer`。只有用户具有内容写入权限（或目录元信息授予写入权限）、通过目录写入白名单、目标不是只读对象，且驱动支持转存时才返回 `true`。调用转存接口时仍会重新校验权限。

## 完成、取消与失败

接口同步等待结果，不创建后台持久化任务。总执行时间上限为 5 分钟；夸克上限为 2 分钟。夸克和阿里云盘的单个异步任务最多轮询 60 次，间隔 1 秒，轮询和 HTTP 请求都携带取消上下文。反向代理的超时应允许等待操作完成。

阿里云盘、百度网盘先读取完整分页，再转存文件。阿里云盘使用目标账号的 drive ID 和目标目录的 file ID；百度网盘使用目标目录在网盘中的实际路径，并以整数保留文件 ID 精度。

失败不回滚已保存的文件。服务端收到失败或成功结果后都会失效目标目录树及容量缓存。连接断开或超时后，上游已提交的任务仍可能继续运行，应重新刷新目录确认结果。转存请求不对网络错误自动重试；手动重试可能产生重复文件。百度返回明确的令牌失效错误时会刷新令牌并重试一次。

## 验证范围

回归测试覆盖模拟上游响应、分页、目标目录参数、提取码、上游失败、异步任务、取消、禁止自动重发，以及 HTTP handler 到文件系统/驱动的权限和能力探测流程。

尚未使用真实账号进行三家网盘联调。上游接口可用性和账号权限限制仍需在实际账号上验证；模拟测试不代表云端接口已验证可用。

```sh
go test ./drivers/base ./drivers/quark_uc ./drivers/aliyundrive ./drivers/baidu_netdisk ./internal/op ./internal/fs ./server/handles
```
