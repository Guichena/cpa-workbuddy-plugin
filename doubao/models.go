package main

import (
	"encoding/json"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// 本文件声明插件对外暴露的模型目录。
//
// 上游 /alice/basic/launch 返回 67 条模型，但它们是内部代号（形如 "Aoe"、
// "ances s2"、"func: ogt（4k）"），与网页端展示名的映射未公开。与其编造映射，
// 不如显式声明一小组稳定语义别名——它们描述的是**能力**而不是厂商 SKU，
// 上游改名时这些别名仍然成立。
//
// 未知名不做拦截，原样透传给上游：上游忽略不认识的模型名并回落到账号默认助手，
// 这比直接报错更符合使用者预期，也让知道内部代号的用户仍可直呼其名。

// modelAlias 是一条对外暴露的模型别名。
type modelAlias struct {
	// ID 是客户端使用的模型名（不含 doubao/ 前缀）。
	ID string
	// DisplayName 是 /v1/models 中的展示名。
	DisplayName string
	// Description 说明该别名对应的能力。
	Description string
	// Skill 是该别名需要激活的上游 skill，0 表示普通对话。
	Skill int
}

// capabilityAliases 是稳定的能力别名目录。
//
// 前缀固定为 doubao/：宿主按 provider 聚合多个插件的模型，裸名容易与其他
// 服务商的同名模型冲突。
var capabilityAliases = []modelAlias{
	{
		ID:          "default",
		DisplayName: "默认助手",
		Description: "账号默认的对话模型，等同于在网页端直接提问。",
	},
	{
		ID:          "pro",
		DisplayName: "深度思考",
		Description: "开启深度思考的对话模型，回复更慢但推理更充分。",
	},
	{
		ID:          "image",
		DisplayName: "图像生成",
		Description: "生成图片。回复以 OpenAI 多模态 content 返回，图片地址在 image_url 中。",
		Skill:       skillImageGen,
	},
	{
		ID:          "video",
		DisplayName: "视频生成",
		Description: "生成视频。视频地址以文本与 media 扩展字段返回。",
		Skill:       skillVideoGen,
	},
}

// modelIDPrefix 是对外模型名的前缀。
const modelIDPrefix = "doubao/"

// referenceCreated 是目录条目的固定创建时间。
//
// 用固定值而非 time.Now()：该字段参与客户端缓存判定，每次轮询都变化会让
// 客户端误以为模型目录在不停更新。
const referenceCreated = 1767225600 // 2026-01-01T00:00:00Z

// normalizeModelName 归一化客户端传入的模型名。
//
// 处理三类常见写法：大小写差异、"doubao/" 前缀、":latest" 后缀。
// [参数] requested: 原始模型名。
// [返回] 去掉前缀与后缀的裸别名（保留原大小写，比较时另行忽略大小写）。
// 最近修改时间：2026-10-17；改动原因：新增，模型名归一化入口。
func normalizeModelName(requested string) string {
	name := strings.TrimSpace(requested)
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	name = strings.TrimSuffix(name, ":latest")
	return name
}

// skillForModel 返回模型名需要激活的上游 skill。
// [参数] requested: 客户端模型名。
// [返回] skill id；普通对话返回 0。
// 最近修改时间：2026-10-17；改动原因：新增，能力映射。
func skillForModel(requested string) int {
	name := normalizeModelName(requested)
	for _, alias := range capabilityAliases {
		if strings.EqualFold(name, alias.ID) {
			return alias.Skill
		}
	}
	return 0
}

// modeForModel 把客户端模型名解析为生成模式。
// [参数] requested: 客户端模型名。
// [返回] 生成模式；未知名回落为普通对话。
// 最近修改时间：2026-10-17；改动原因：新增，请求构造的模型入口。
func modeForModel(requested string) generationMode {
	name := normalizeModelName(requested)
	for _, alias := range capabilityAliases {
		if strings.EqualFold(name, alias.ID) {
			return generationMode{Skill: alias.Skill, Kind: kindForAlias(alias)}
		}
	}
	return textMode()
}

// kindForAlias 返回别名对应的回复类型。
// [参数] alias: 别名。
// [返回] text / image / video。
// 最近修改时间：2026-10-17；改动原因：新增，回复解析类型判定。
func kindForAlias(alias modelAlias) string {
	switch alias.Skill {
	case skillImageGen:
		return "image"
	case skillVideoGen:
		return "video"
	}
	return "text"
}

// catalogueModels 构造完整的模型目录。
// [参数] 无。
// [返回] 宿主模型结构列表。
// 最近修改时间：2026-10-17；改动原因：新增，目录构造。
func catalogueModels() []pluginapi.ModelInfo {
	out := make([]pluginapi.ModelInfo, 0, len(capabilityAliases))
	for _, alias := range capabilityAliases {
		info := pluginapi.ModelInfo{
			ID:          modelIDPrefix + alias.ID,
			Object:      "model",
			Created:     referenceCreated,
			OwnedBy:     providerName,
			Type:        "chat",
			DisplayName: alias.DisplayName,
			Name:        alias.ID,
			Description: alias.Description,
		}
		if alias.Skill == skillImageGen || alias.Skill == skillVideoGen {
			info.Type = "image"
			if alias.Skill == skillVideoGen {
				info.Type = "video"
			}
		}
		out = append(out, info)
	}
	return out
}

// filterExcludedModels 剔除宿主 oauth-excluded-models 中为本插件列出的模型。
//
// 宿主只在 model.for_auth 的按账号路径上替插件过滤；model.static 的返回会被原样
// 登记进全局模型目录，因此插件必须在出口自行过滤——这是同名模型被多个 provider
// 同时认领时唯一能把模型从本服务商摘掉的入口。
// [参数] models: 已解析的模型列表；host: 宿主配置摘要。
// [返回] 过滤后的列表；未配置排除项时原样返回入参。
// 最近修改时间：2026-10-17；改动原因：新增，与既有插件保持一致的排除语义。
func filterExcludedModels(models []pluginapi.ModelInfo, host pluginapi.HostConfigSummary) []pluginapi.ModelInfo {
	if len(host.ExcludedModels) == 0 {
		return models
	}
	excluded := host.ExcludedModels[providerName]
	if len(excluded) == 0 {
		for channel, list := range host.ExcludedModels {
			if strings.EqualFold(strings.TrimSpace(channel), providerName) {
				excluded = list
				break
			}
		}
	}
	if len(excluded) == 0 {
		return models
	}

	excludeSet := make(map[string]struct{}, len(excluded))
	for _, m := range excluded {
		excludeSet[strings.ToLower(strings.TrimSpace(m))] = struct{}{}
	}

	// 返回新切片而非就地压缩：入参可能来自共享缓存，就地写回会把缓存
	// 永久变成「已过滤列表」。
	out := make([]pluginapi.ModelInfo, 0, len(models))
	for _, m := range models {
		if _, skip := excludeSet[strings.ToLower(m.ID)]; skip {
			continue
		}
		out = append(out, m)
	}
	return out
}

// modelStatic 响应 model.register / model.static。
//
// 宿主在启动期与按需调用本方法，此时可能还没有任何账号；目录必须照常返回，
// 否则用户在授权前没有模型可选。
// [参数] request: 宿主请求体。
// [返回] 信封 JSON 与错误。
// 最近修改时间：2026-10-17；改动原因：新增，静态目录入口。
func modelStatic(request []byte) ([]byte, error) {
	var req pluginapi.StaticModelRequest
	if len(request) > 0 {
		_ = json.Unmarshal(request, &req)
	}
	return okEnvelope(pluginapi.ModelResponse{
		Provider: providerName,
		Models:   filterExcludedModels(catalogueModels(), req.Host),
	})
}

// modelForAuth 响应 model.for_auth。
//
// 豆包各账号共享同一套能力目录（差别在账号额度而非模型），因此每个账号都返回
// 全量目录；宿主会按 provider 合并去重。
// [参数] request: 宿主请求体。
// [返回] 信封 JSON 与错误。
// 最近修改时间：2026-10-17；改动原因：新增，按账号目录入口。
func modelForAuth(request []byte) ([]byte, error) {
	var req pluginapi.AuthModelRequest
	if len(request) > 0 {
		_ = json.Unmarshal(request, &req)
	}
	return okEnvelope(pluginapi.ModelResponse{
		Provider: providerName,
		Models:   filterExcludedModels(catalogueModels(), req.Host),
	})
}
