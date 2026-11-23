# 火山方舟 Seedream 合成接入

## 链路

```
小程序 选择发型
   → POST /api/v1/hairstyles/tasks        (只落库，立即返回 task_id)
   → 后台 Worker 每 2s 轮询 design_tasks
        claim → 读原图 → enrichFromAnalysis(脸型/发际线/发量)
        → VolcGenerator.Generate → 下载临时 URL → PhotoService.Save
        → design_results(result_type=image, storage_key)
   → GET /api/v1/design-tasks/:id          轮询状态
   → GET /api/v1/design-tasks/:id/image?token=...   取图
```

## 配置

`.env`：

```
AI_PROVIDER=volc
AI_API_KEY=<方舟 API Key>
AI_MODEL=doubao-seedream-4-0-250828
AI_BASE_URL=https://ark.cn-beijing.volces.com/api/v3
AI_TIMEOUT_SECONDS=120
AI_IMAGE_SIZE=1024x1280
```

`AI_MODEL` 建议填控制台「在线推理」里的接入点 ID（形如 `ep-2025...`），
这样换模型不用改代码。

## 提示词策略

`internal/ai/volc_generator.go` 的 `buildVolcPrompt`：

1. **先锁脸**：首句固定「保持原图人物的五官、脸型、肤色、表情完全不变，只替换头发部分」。
   Seedream 是整图重绘，不写死脸会被顺手改掉。
2. **只写实测条件**：发际线百分比、发量档位取不到就整句不写，不做默认填充。
3. **负向词折进正向**：方舟没有 `negative_prompt`，用「避免出现：xxx」表达。
4. 穿着固定为白色圆领上衣，背景保留原样，减少模型自由发挥。

## 参数与成本相关

| 参数 | 值 | 说明 |
|---|---|---|
| `sequential_image_generation` | `disabled` | 不写会按组图出长图序列，费用翻倍 |
| `response_format` | `url` | 只返回 24h 临时 URL，必须同请求内下载转存 |
| `watermark` | `false` | 不加「AI 生成」水印 |
| `size` | `1024x1280` | 4:5 半身像，按尺寸计价 |

发送前原图统一缩到长边 1280（`downscaleJPEG`），避免 base64 后请求体过大被断连。

## 失败处理

- 429/5xx：重试 1 次
- 4xx：不重试（多为参数或审核问题，重试只重复计费）
- 出图成功但落盘失败：任务仍判成功，`suggestion.image_save_failed=true`，前端回落到方案文字

## 测试

```bash
go test ./internal/ai/...
```

覆盖：请求体结构、缩图、错误透出、4xx 不重试、429 重试一次、缺 key 立即失败、PNG 输入。

## 待补

- 积分扣减（分析 3 / 合成 30）
- 审核失败的用户侧文案（现在只回「合成失败」）
- 重新生成（同一任务二次出图，走 `/regenerate`）
