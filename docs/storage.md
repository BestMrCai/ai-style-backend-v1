# 对象存储（七牛云 / 本地）

## 目录约定

| 内容 | key 前缀 | 谁写 |
|---|---|---|
| 用户上传的原图 | `upload/user/<YYYYMMDD>/<random>.jpg` | 上传接口 |
| 人脸标注图 | `upload/face_analyses/<YYYYMMDD>/<random>.jpg` | 面部分析 Worker |
| AI 合成效果图 | `upload/face_analyses/<YYYYMMDD>/<random>.jpg` | 设计任务 Worker |

日期子目录按写入时刻生成，不是按请求时间。单目录堆几十万文件会让
本地 `ls` 和 OSS 控制台都很难用，按天切分是最省事的办法。

标注图和效果图共用 `upload/face_analyses`：两者都是服务端产物、
生命周期一致，也都只有本人能看，没必要再拆。

## 抽象层

`internal/storage`：

```
Storage 接口
  Put(ctx, key, data) error
  Get(ctx, key) ([]byte, error)
  Delete(ctx, key) error
  Driver() string

LocalStorage   → 本地磁盘（开发）
QiniuStorage   → 七牛云（线上）
```

业务层（`PhotoService`、各 Worker、各 Handler）只依赖 `Storage`，
换阿里云 OSS 或腾讯云 COS 时，只新增一个实现并在 `main.go` 换装配。

## 配置

```bash
STORAGE_DRIVER=local          # local | qiniu
STORAGE_LOCAL_ROOT=.          # 本地根目录

QINIU_ACCESS_KEY=
QINIU_SECRET_KEY=
QINIU_BUCKET=
QINIU_DOMAIN=https://cdn.example.com
QINIU_UPLOAD_URL=https://up.qiniup.com
QINIU_API_HOST=https://rs.qiniuapi.com
```

`QINIU_UPLOAD_URL` 必须按存储区域选，否则会报 400：

| 区域 | 上传域名 |
|---|---|
| 华东 | `https://up.qiniup.com` |
| 华北 | `https://up-z1.qiniup.com` |
| 华南 | `https://up-z2.qiniup.com` |
| 北美 | `https://up-na0.qiniup.com` |
| 东南亚 | `https://up-as0.qiniup.com` |

`QINIU_DOMAIN` 填绑定域名，**带协议、不带结尾斜杠**。
测试环境直接用七牛分配的测试域名（形如 `http://xxx.bkt.clouddn.com`）也可以，
但测试域名有有效期，且可能被限速，上线前要换自己的域名。

## 为什么自己实现而不用官方 SDK

只用到三个动作：上传凭证签名、表单上传、管理接口删除。
标准库的 `crypto/hmac` + `net/http` + `mime/multipart` 足够，
引入 SDK 会带来整棵依赖树，而这里只需要一百多行。

签名有两套，容易混：

| 用途 | 算法 | 说明 |
|---|---|---|
| 上传凭证 | `HMAC-SHA1(SecretKey, base64url(putPolicy))` | 拼成 `AK:sign:policy` |
| 管理接口 | `HMAC-SHA1(SecretKey, "METHOD path\nHost\nContent-Type\n\n")` | 头 `Authorization: QBox AK:sign` |

上传凭证的 `scope` 写死 `bucket:key`，凭证只能写这一个对象，
即使泄露也无法覆盖别人的文件。`deadline` 设 1 小时。

## 读图为什么走服务端代理

小程序 `<image>` 无法自定义请求头，如果直连 OSS：

- 私有空间要生成带签名的临时 URL，还得处理过期；
- 公有空间则任何人拿到 URL 都能看，用户照片会泄露。

所以统一走 `GET /api/v1/face-analyses/:id/source?token=...` 这类接口，
服务端校验 token 后再从存储读取并返回。代价是多一次转发，
但 MVP 阶段图片不大，且能顺便做访问控制。

## 老数据兼容

历史记录是 `uploads/xxx.jpg`（平铺，无日期目录）。
`LocalStorage` 的根目录默认 `.`，所以：

- 新数据 `upload/user/20261118/x.jpg` → `<root>/upload/user/...`
- 老数据 `uploads/x.jpg` → `<root>/uploads/x.jpg`

两者在同一层解析，不用迁移。`ValidateKey` 也同时放行 `upload/` 和 `uploads/`。

## 私有空间（实测踩过的坑）

七牛私有空间的下载凭证，签名原文必须是**完整 URL**：

```
sign = base64url(HMAC-SHA1(SecretKey, "http://cdn.example.com/upload/user/20261118/a.jpg?e=1763500000"))
最终 URL: http://cdn.example.com/upload/user/20261118/a.jpg?e=1763500000&token=<AK>:<sign>
```

官方文档里的示例写的是 `"<path>?e=<deadline>"`，但实测以完整 URL 为准。
用相对路径签名会得到：

```
401 {"error":"download token auth failed"}
```

这是本项目的实际调试结论，不要照抄文档里的示例。

### 自动识别

`QINIU_PRIVATE` 有三个取值：

| 值 | 行为 |
|---|---|
| `auto`（默认） | 先直连，收到 401 后自动改用签名重试，并记住结果 |
| `true` | 始终带签名，省掉第一次探测 |
| `false` | 始终直连，公有空间用 |

`auto` 对公有空间没有额外开销（一次请求成功即结束），
对私有空间也只是第一次多一跳，之后直接走签名。

### 调试时要注意 CDN 缓存

同一个 key 被成功读取一次后，后续请求会命中七牛 CDN 缓存，
即使去掉 token 也返回 200。验证签名时**必须用新的 key**，
否则会误判成「签名没生效也照读」。

## 上线到七牛的检查清单

1. 建空间，记下 bucket 名和绑定域名
2. 密钥管理里拿 AK/SK（建议用子账号，只授对象存储权限）
3. 确认空间是**公有**还是**私有**，据此设置 `QINIU_PRIVATE`
   - 公有：`QINIU_PRIVATE=false`
   - 私有：`QINIU_PRIVATE=true`（或留 auto）
4. 按区域填 `QINIU_UPLOAD_URL`
5. 跑一次上传，确认 key 形如 `upload/user/20261118/xxx.jpg`
6. 确认小程序读图正常（走服务端代理，不直连域名）

私有空间下载签名已实现（对完整 URL 签名），实测通过。
