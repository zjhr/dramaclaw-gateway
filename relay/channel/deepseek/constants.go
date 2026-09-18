package deepseek

// 首位两个与官方 /v1/models 对齐（2026-09 实测只返回它们）。
// chat/reasoner 与 -none/-max 变体保留：仍可经 NewAPI 路由调用，只是官方列举接口
// 不再返回；删掉会让在用的部署失去这些名字的能力推断。该列表只用于能力推断，
// 不决定实际可调用的模型（那由渠道声明的 models 与路由表决定）。
var ModelList = []string{
	"deepseek-flash", "deepseek-v4-pro",
	"deepseek-chat", "deepseek-reasoner",
	"deepseek-v4-flash", "deepseek-v4-flash-none", "deepseek-v4-flash-max",
	"deepseek-v4-pro-none", "deepseek-v4-pro-max",
}

var ChannelName = "deepseek"
