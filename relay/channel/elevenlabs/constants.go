package elevenlabs

// ElevenLabs 渠道常量。
//
// 上游认证走 `xi-api-key`（不是 Authorization: Bearer），且语音合成的 voice_id
// 在**路径**里（/v1/text-to-speech/{voice_id}），这两点与其它渠道不同。
//
// 模型名分两类用途：
//   - 逻辑名（ModelMusic/ModelTTS/ModelSoundEffect）：DramaClaw 媒体模型映射里
//     的上游名，用来在适配器内分流到不同端点；
//   - 上游 model_id（如 music_v2_5）：ElevenLabs 自己的模型标识，随 body 发送。
//
// 音效没有模型概念，端点即能力，所以用一个固定的逻辑名占位。
const (
	ChannelName = "elevenlabs"

	// 逻辑名：决定走哪个端点。
	ModelMusic       = "elevenlabs-music"
	ModelTTS         = "elevenlabs-tts"
	ModelSoundEffect = "elevenlabs-sound-effect"

	// 上游 model_id 默认值，可被 metadata.model_id 覆盖。
	DefaultMusicModelId = "music_v2_5"
	DefaultTTSModelId   = "eleven_turbo_v2_5"

	pathMusic         = "/v1/music"
	pathTextToSpeech  = "/v1/text-to-speech/"
	pathSoundGenerate = "/v1/sound-generation"
	pathModels        = "/v1/models"

	defaultMusicOutputFormat = "mp3_44100_128"
)

// ModelList 供渠道路由与「供应商渠道管理」列表展示。
var ModelList = []string{
	ModelMusic,
	ModelTTS,
	ModelSoundEffect,
}
