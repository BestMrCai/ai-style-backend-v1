# 发型推荐与合成

## 这一期做了什么

| 能力 | 状态 |
|---|---|
| 性别（必选）/ 年龄段（选填） | 已上线 |
| 发型素材库（男女各 10 款） | 已上线 |
| 基于分析结果的推荐打分 | 已上线 |
| 异步发型任务 + 结果页 | 已上线，返回方案说明 |
| 真实图像合成 | 未开启，AI_PROVIDER=mock |
| 自定义上传参考图 | 未开启，design_mode=reference 已就绪 |
| u18 染烫推荐 | 策略关闭，改配置即可开 |

## 接口

### 1. 创建面部分析（性别必填）

    POST /api/v1/face-analyses
    {
      "photo_id": 43,
      "gender": "female",       // male / female，必填
      "age_group": "26_35"      // 选填，空字符串表示不填
    }

年龄段取值：u18 / 18_25 / 26_35 / 36_45 / 46_55 / 56p

### 2. 补填 / 修改性别与年龄段

    PATCH /api/v1/face-analyses/:id/profile
    { "gender": "female", "age_group": "u18" }

只影响之后的推荐，不重新跑检测，报告结论不变。

### 3. 获取推荐

    GET /api/v1/hairstyles/recommendations?analysis_id=20&limit=5

响应示例：

    {
      "summary": "结合鹅蛋脸与圆弧上拱，这几款的轮廓关系更协调",
      "items": [
        {
          "style_id": "f_curtain_bangs",
          "name": "八字刘海中长发",
          "cover_url": "/api/v1/hairstyles/f_curtain_bangs/cover",
          "score": 5.3,
          "reasons": ["与鹅蛋脸的轮廓关系比较协调"],
          "requires": ["cut"],
          "school_friendly": true
        }
      ]
    }

409 表示分析未完成或缺性别，前端据此引导补选。

### 4. 创建合成任务

    POST /api/v1/hairstyles/tasks
    { "analysis_id": 20, "style_id": "f_curtain_bangs" }

立即返回 task_id，任务由后台 Worker 处理，小程序可返回首页。

### 5. 查询结果

    GET /api/v1/design-tasks/:id

results[0].suggestion.hairstyle.basis 会原样带回合成时使用的条件，
便于确认条件确实传到了生成层。

## 打分规则

加分：

| 维度 | 权重 |
|---|---|
| 脸型匹配（fit_shapes） | 3.0 |
| 发际线形态匹配（fit_hairline） | 1.5 |
| 发际线高度匹配（fit_hairline_level） | 1.2 |
| 肤色冷暖（fit_tone） | 0.8 |
| 发量档位（fit_density） | 0.6 |
| u18 校园场景 | 0.4 |

减分：

| 维度 | 权重 |
|---|---|
| 脸型不建议（avoid_shapes） | -2.0 |
| 发际线高度不建议（avoid_hairline_level） | -1.2 |
| 发际线形态不建议（avoid_hairline） | -1.0 |

减分只降低排序，不生成负面推荐理由；被减分的素材
不会被“通用造型”兜底逻辑抬回正分。

同分按 style_id 升序，保证刷新顺序稳定。

## 发际线高度与发量

面部检测服务会在报告的 `hair` 节输出：

    "hairline_height_level": "偏低" | "适中" | "偏高",
    "hairline_height_value": 29.1,          // 上庭比例，占脸长百分比
    "density": "稀疏" | "适中" | "浓密",         // 取不到时为空串
    "density_text": "浓密",              // 展示用，取不到时为“照片中暂无法判断”

高度档位阈值：上庭比例 < 30% 为偏低，≥ 36% 为偏高。
发量只做三档，由发顶连续覆盖高度（0.7）与纹理密度（0.3）加权得出，
取不到就输出空串，不编造数值。

## 年龄段策略

internal/hairstyle/library.go：

    var agePolicies = map[string]AgePolicy{
        "u18": {AllowColor: false, AllowPerm: false},
        // 其余全部 true
    }

u18 当前不推荐需要染发或烫发的发型，但素材与代码路径全部保留。
后续放开只改这两个布尔值，不动业务逻辑。

## 发型素材

data/hairstyles/library.json，字段说明：

| 字段 | 说明 |
|---|---|
| gender | 主性别 |
| fit_gender | 允许的性别，中性款可填两个 |
| fit_shapes | 命中脸型结论时加分 |
| fit_hairline | 命中发际线形态时加分 |
| fit_hairline_level | 命中发际线高度档位时加分 |
| avoid_shapes | 命中脸型时扣分 |
| avoid_hairline | 命中发际线形态时扣分 |
| avoid_hairline_level | 命中发际线高度档位时扣分 |
| fit_density | 命中发量档位时加分 |
| fit_tone | 命中肤色冷暖时加分 |
| requires | cut / perm / color |
| school_friendly | 是否适合校园场景 |
| prompt_hint / negative_hint | 供后续图像合成使用，本期不调用 |
| volume_weight | 对发量的要求，影响合成蓬松度 |

封面图查找顺序：

    uploads/rec              ← 运营直接放图，优先
    data/hairstyles/covers   ← 兜底占位图

文件名必须是 `{style_id}.jpg`，支持 .jpg / .jpeg / .png / .webp。
放进去无需重启，刷新列表即生效；删掉则回落到占位图，不会出现空白。
命名规则与完整 style_id 清单见 `uploads/rec/README.md`。

## 后续接真实合成

生成器只需要实现 ai.Generator：

    type Generator interface {
        Generate(ctx context.Context, request GenerateRequest) (*GenerateResult, error)
    }

GenerateRequest 已经包含 FaceShape、Hairline、HairlineHeightPercent、
Density、Gender、AgeGroup、PromptHint、NegativeHint、VolumeWeight。

合成时的关键约束：

- 遮罩只覆盖原发区，脸与五官不重绘
- 遮罩上边界取实测发际线高度，不让模型自由发挥
- 取不到的字段留空，不做默认填充

