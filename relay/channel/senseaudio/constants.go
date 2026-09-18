package senseaudio

import "time"

// SenseAudio 渠道常量。
//
// 各能力的上游协议差异很大，全部由本渠道适配器吸收：
//   - 音乐：**异步任务**（song/create → pending 轮询）
//   - 语音：同步（/v1/t2a_v2）
//   - 音效：同步（/v1/sound-effects/generations）
//   - 图片：同步或异步（/v1/image/sync | /v1/image/async + pending）
//
// DramaClaw 侧只发 DC-Media 统一协议，模型名用于在适配器内分流。
const (
	ChannelName = "senseaudio"

	// ---- 音乐 ----
	ModelMusicV1 = "senseaudio-music-1.0-260319"
	ModelMusicV2 = "senseaudio-music-2.0-260626"

	// ---- 语音合成 ----
	ModelTTS15     = "senseaudio-tts-1.5-260319"
	ModelTTSNova20 = "sensenova-tts-2.0"

	// ---- 音效 ----
	ModelSoundEffect = "senseaudio-sfx-1.0"

	// ---- 图片 ----
	ModelImage20      = "senseaudio-image-2.0-260319"
	ModelImageSeedream = "doubao-seedream-5-0-260128"
	ModelImageNovaFast = "sensenova-u1-fast"

	// ---- 视频 ----
	ModelVideoSeedance = "doubao-seedance-2-0-260128"
)

// 上游端点。除音乐外都用各自的专用路径。
const (
	pathMusicCreateV1 = "/v1/music/song/create"
	pathMusicCreateV2 = "/v2/music/song/create"
	pathMusicPending  = "/v1/music/song/pending/"

	pathTTSSynthesize = "/v1/t2a_v2"

	pathSoundEffect = "/v1/sound-effects/generations"

	pathImageSync    = "/v1/image/sync"
	pathImageAsync   = "/v1/image/async"
	pathImagePending = "/v1/image/pending"

	pathVideoCreate = "/v1/video/create"
	pathVideoStatus = "/v1/video/status"

	statusPending   = "PENDING"
	statusSuccess   = "SUCCESS"
	statusFailed    = "FAILED"
	statusCompleted = "completed"
)

// 音乐生成通常 30s~3min，给足余量但必须封顶——DoResponse 是同步等待的，
// 无限轮询会一直占着连接。
const (
	pollInterval = 3 * time.Second
	pollTimeout  = 10 * time.Minute
)

// ModelList 供渠道路由与「供应商渠道管理」列表展示。
var ModelList = []string{
	ModelMusicV1,
	ModelMusicV2,
	ModelTTS15,
	ModelTTSNova20,
	ModelSoundEffect,
	ModelImage20,
	ModelImageSeedream,
	ModelImageNovaFast,
	ModelVideoSeedance,
}
