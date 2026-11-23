# AI Style Backend V1 (Windows + Go + Gin + PostgreSQL)

第一阶段目标：启动 Go API、连接 PostgreSQL，并提供 `/health` 与 `/ready`。

## 1. 安装前置软件

安装：
- Go（使用 go.dev/dl 的稳定版）
- Docker Desktop（推荐用于本地 PostgreSQL），并启动 Docker Desktop
- Git（可选）

安装后在 PowerShell 验证：
```powershell
go version
docker --version
docker compose version
```

## 2. 配置环境变量

在项目根目录复制配置模板：
```powershell
Copy-Item .env.example .env
```
当前 `.env` 是本地开发示例。不要把真实密码、云密钥或 `.env` 提交到 Git。

## 3. 启动 PostgreSQL

在项目根目录运行：
```powershell
docker compose up -d postgres
docker compose ps
```

查看数据库日志：
```powershell
docker compose logs -f postgres
```
退出日志查看按 `Ctrl+C`，不会停止数据库容器。

## 4. 下载 Go 依赖

```powershell
go mod tidy
```

## 5. 执行数据库迁移

安装 golang-migrate CLI（Windows 可通过其官方发布页下载 Windows amd64 可执行文件）。把 `migrate.exe` 放进 PATH，重新打开 PowerShell 后检查：
```powershell
migrate -version
```

在项目根目录执行：
```powershell
migrate -path migrations -database "postgres://ai_style:change_me_local@localhost:5432/ai_style_db?sslmode=disable" up
```

查看当前迁移版本：
```powershell
migrate -path migrations -database "postgres://ai_style:change_me_local@localhost:5432/ai_style_db?sslmode=disable" version
```

注意：迁移连接串中的用户名、密码、数据库名必须与 `.env` 和 `docker-compose.yml` 一致。若密码含有特殊字符，需要进行 URL 编码。

## 6. 启动 API

```powershell
go run ./cmd/server
```

预期日志类似：
```text
PostgreSQL connection established
HTTP server listening on http://localhost:8080
```

## 7. 测试接口

在浏览器打开：
- http://localhost:8080/health
- http://localhost:8080/ready

或使用 PowerShell：
```powershell
Invoke-RestMethod http://localhost:8080/health
Invoke-RestMethod http://localhost:8080/ready
```

`/health` 检查进程是否能响应；`/ready` 额外检查数据库连接。

## 8. 常见问题

### `go` 不是内部或外部命令
安装 Go 后关闭并重新打开 PowerShell，再运行 `go version`。

### 数据库连接失败 / connection refused
确认 Docker Desktop 已启动，然后运行 `docker compose ps`。确认 5432 端口没有被其他 PostgreSQL 实例占用。

### password authentication failed
确认 `.env` 与 `docker-compose.yml` 的数据库凭据一致。Docker 数据卷已经初始化后，修改 compose 密码不会自动修改旧数据库用户密码。仅在本地无重要数据时，才考虑删除数据卷重新初始化：
```powershell
docker compose down -v
docker compose up -d postgres
```
`down -v` 会删除本地数据库数据，请谨慎执行。

### migrate: command not found
确认 `migrate.exe` 所在目录已加入 PATH，并重新打开终端。

## 9. 当前阶段的范围

已预留用户、照片、设计任务、设计结果、额度和额度流水表；当前尚未实现微信登录、上传凭证、AI 生成、会员支付和业务 API。后续按模块逐个开发。

生产部署前还需增加：HTTPS、正式密钥管理、数据库迁移发布流程、请求限流、日志脱敏、备份恢复、权限校验与隐私删除流程。

## 10. 脸型分析（任务化，异步）

流程：小程序上传照片 → 创建分析任务 → 等待页/首页历史 → 分析完成后查看报告。

### 10.1 依赖 Python face-server

```powershell
# 在 D:\codex-project\ai-styple 启动（full 模式才是真实 MediaPipe）
docker compose --profile full up -d
# 确认 http://localhost:8001/health 的 detector.resolved 为 full
```

`.env` 中配置：

```dotenv
FACE_SERVICE_URL=http://localhost:8001
FACE_TIMEOUT_SECONDS=90
```

### 10.2 数据库迁移

```powershell
migrate -path migrations -database "postgres://ai_style:change_me_local@localhost:5432/ai_style_db?sslmode=disable" up
```

新增表 `face_analyses`（迁移 000004）。

### 10.3 接口

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/face-analyses` | 创建任务，body `{ "photo_id": 1 }` |
| GET | `/api/v1/face-analyses` | 历史列表（不含完整报告） |
| GET | `/api/v1/face-analyses/:id` | 任务状态；succeeded 时带 `result` |
| GET | `/api/v1/face-analyses/:id/overlay` | 标注图 |
| GET | `/api/v1/face-analyses/:id/source` | 原始照片 |

图片接口支持 `Authorization: Bearer` 或 `?token=`，后者用于小程序 `<image>` 组件。

Python 服务不可达时，任务会被标记为 `failed`（`error_code=FACE_SERVICE_UNAVAILABLE`），不会静默产出假数据。
