package senseaudio

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// 音频类能力的上游协议彼此不兼容，这里按模型名分流到各自的端点与请求结构。
// 三者都走 DC-Media 的 /v1/audio/speech 语义（RelayModeAudioSpeech）。
type audioKind int

const (
	audioKindMusic audioKind = iota
	audioKindTTS
	audioKindSFX
)

func resolveAudioModel(model string) (audioKind, string, bool) {
	switch strings.TrimPrefix(strings.TrimSpace(model), "/") {
	case ModelMusicV1:
		return audioKindMusic, pathMusicCreateV1, true
	case ModelMusicV2:
		return audioKindMusic, pathMusicCreateV2, true
	case ModelTTS15, ModelTTSNova20:
		return audioKindTTS, pathTTSSynthesize, true
	case ModelSoundEffect:
		return audioKindSFX, pathSoundEffect, true
	default:
		return 0, "", false
	}
}

// ---- 音乐 ----
// V1 用历史参数（instrumental 布尔），V2 用结构化参数（mode 枚举 + 嵌套
// audio_settings）。两版字段不通用，不能合并成一个结构体。
type musicV1Request struct {
	Model        string `json:"model"`
	Lyrics       string `json:"lyrics,omitempty"`
	CustomMode   *bool  `json:"custom_mode,omitempty"`
	Instrumental *bool  `json:"instrumental,omitempty"`
	Style        string `json:"style,omitempty"`
	Title        string `json:"title,omitempty"`
}

type musicV2AudioSettings struct {
	Format string `json:"format,omitempty"`
}

type musicV2Request struct {
	Model         string                `json:"model"`
	Prompt        string                `json:"prompt,omitempty"`
	Mode          string                `json:"mode,omitempty"`
	Style         string                `json:"style,omitempty"`
	Lyrics        string                `json:"lyrics,omitempty"`
	AudioSettings *musicV2AudioSettings `json:"audio_settings,omitempty"`
}

type musicCreateResponse struct {
	TaskId string `json:"task_id"`
}

type musicPendingAudio struct {
	AudioUrl string `json:"audio_url"`
	Title    string `json:"title"`
	Duration int    `json:"duration"`
}

type musicPendingResponse struct {
	TaskId     string `json:"task_id"`
	Status     string `json:"status"`
	FailReason string `json:"fail_reason"`
	Response   struct {
		Data []musicPendingAudio `json:"data"`
	} `json:"response"`
}

// ---- 语音合成 ----
type ttsVoiceSetting struct {
	VoiceId string   `json:"voice_id,omitempty"`
	Speed   *float64 `json:"speed,omitempty"`
	Vol     *float64 `json:"vol,omitempty"`
	Pitch   *int     `json:"pitch,omitempty"`
}

type ttsAudioSetting struct {
	Format     string `json:"format,omitempty"`
	SampleRate *int   `json:"sample_rate,omitempty"`
	Bitrate    *int   `json:"bitrate,omitempty"`
	Channel    *int   `json:"channel,omitempty"`
}

type ttsRequest struct {
	Model        string           `json:"model"`
	Text         string           `json:"text"`
	Stream       bool             `json:"stream"`
	VoiceSetting *ttsVoiceSetting `json:"voice_setting,omitempty"`
	AudioSetting *ttsAudioSetting `json:"audio_setting,omitempty"`
}

type ttsResponse struct {
	Data struct {
		Audio           string `json:"audio"`
		AudioLength     int    `json:"audio_length"`
		AudioSampleRate int    `json:"audio_sample_rate"`
	} `json:"data"`
	BaseResp struct {
		StatusCode int    `json:"status_code"`
		StatusMsg  string `json:"status_msg"`
	} `json:"base_resp"`
}

// ---- 音效 ----
type sfxRequest struct {
	Text            string   `json:"text"`
	Model           string   `json:"model"`
	VariantsCount   *int     `json:"variants_count,omitempty"`
	DurationSeconds *int     `json:"duration_seconds,omitempty"`
	SmartDuration   *bool    `json:"smart_duration,omitempty"`
	PromptInfluence *float64 `json:"prompt_influence,omitempty"`
	OutputFormat    string   `json:"output_format,omitempty"`
}

type sfxResponse struct {
	GenerationId string `json:"generation_id"`
	Status       string `json:"status"`
	Items        []struct {
		Id              string `json:"id"`
		Status          string `json:"status"`
		AudioUrl        string `json:"audio_url"`
		DurationSeconds int    `json:"duration_seconds"`
		OutputFormat    string `json:"output_format"`
		FailReason      string `json:"fail_reason"`
	} `json:"items"`
}

// DC-Media 协议的 metadata 子集。music_length_ms 上游两版都没有对应参数，
// 显式读出来是为了在日志里可追溯，不透传——假装它生效比丢掉更糟。
type audioMetadata struct {
	MusicLengthMS            *int     `json:"music_length_ms,omitempty"`
	ForceInstrumental        *bool    `json:"force_instrumental,omitempty"`
	RespectSectionsDurations *bool    `json:"respect_sections_durations,omitempty"`
	OutputFormat             string   `json:"output_format,omitempty"`
	Style                    string   `json:"style,omitempty"`
	Title                    string   `json:"title,omitempty"`
	DurationSeconds          *int     `json:"duration_seconds,omitempty"`
	SmartDuration            *bool    `json:"smart_duration,omitempty"`
	PromptInfluence          *float64 `json:"prompt_influence,omitempty"`
	VariantsCount            *int     `json:"variants_count,omitempty"`
	Speed                    *float64 `json:"speed,omitempty"`
	Pitch                    *int     `json:"pitch,omitempty"`
	Volume                   *float64 `json:"volume,omitempty"`
}

func parseAudioMetadata(request dto.AudioRequest) (audioMetadata, error) {
	var metadata audioMetadata
	if len(request.Metadata) == 0 {
		return metadata, nil
	}
	if err := common.Unmarshal(request.Metadata, &metadata); err != nil {
		return metadata, fmt.Errorf("invalid senseaudio audio metadata: %w", err)
	}
	return metadata, nil
}

// normalizeAudioFormat 只保留上游 audio_settings.format 认的枚举值。
func normalizeAudioFormat(responseFormat string) string {
	switch strings.ToLower(strings.TrimSpace(responseFormat)) {
	case "", "mp3":
		return "mp3"
	case "wav":
		return "wav"
	default:
		return ""
	}
}

func buildAudioRequest(model string, request dto.AudioRequest) (any, error) {
	kind, _, ok := resolveAudioModel(model)
	if !ok {
		return nil, fmt.Errorf("unsupported senseaudio audio model: %s", model)
	}
	input := strings.TrimSpace(request.Input)
	if input == "" {
		return nil, fmt.Errorf("input is required for senseaudio audio")
	}
	metadata, err := parseAudioMetadata(request)
	if err != nil {
		return nil, err
	}
	format := normalizeAudioFormat(request.ResponseFormat)

	switch kind {
	case audioKindMusic:
		return buildMusicRequest(model, input, metadata, format)
	case audioKindTTS:
		voice := strings.TrimSpace(request.Voice)
		if voice == "" {
			return nil, fmt.Errorf("voice is required for senseaudio tts")
		}
		req := &ttsRequest{
			Model:  model,
			Text:   input,
			Stream: false,
			VoiceSetting: &ttsVoiceSetting{
				VoiceId: voice,
				Speed:   metadata.Speed,
				Vol:     metadata.Volume,
				Pitch:   metadata.Pitch,
			},
		}
		if format != "" {
			req.AudioSetting = &ttsAudioSetting{Format: format}
		}
		return req, nil
	case audioKindSFX:
		req := &sfxRequest{
			Text:            input,
			Model:           model,
			DurationSeconds: metadata.DurationSeconds,
			SmartDuration:   metadata.SmartDuration,
			PromptInfluence: metadata.PromptInfluence,
			VariantsCount:   metadata.VariantsCount,
		}
		// 上游枚举只有 mp3/wav，拿不准就别发。
		if format != "" {
			req.OutputFormat = format
		}
		return req, nil
	default:
		return nil, fmt.Errorf("unsupported senseaudio audio model: %s", model)
	}
}

func buildMusicRequest(model string, input string, metadata audioMetadata, format string) (any, error) {
	instrumental := metadata.ForceInstrumental != nil && *metadata.ForceInstrumental
	switch model {
	case ModelMusicV1:
		// V1 没有 prompt 字段：用 lyrics 承载提示词，custom_mode=false 表示
		// 这不是用户自备歌词，而是自然语言描述。
		customMode := false
		req := &musicV1Request{
			Model:      ModelMusicV1,
			Lyrics:     input,
			CustomMode: &customMode,
			Style:      metadata.Style,
			Title:      metadata.Title,
		}
		if metadata.ForceInstrumental != nil {
			req.Instrumental = metadata.ForceInstrumental
		}
		return req, nil
	case ModelMusicV2:
		req := &musicV2Request{
			Model:  ModelMusicV2,
			Prompt: input,
			Style:  metadata.Style,
		}
		if instrumental {
			req.Mode = "instrumental"
		}
		if format != "" {
			req.AudioSettings = &musicV2AudioSettings{Format: format}
		}
		return req, nil
	default:
		return nil, fmt.Errorf("unsupported senseaudio music model: %s", model)
	}
}

// handleAudioResponse 按模型能力分派到各自的响应处理。
func handleAudioResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	defer func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()
	model := musicModelName(info, dto.AudioRequest{})
	kind, _, ok := resolveAudioModel(model)
	if !ok {
		return nil, types.NewOpenAIError(
			fmt.Errorf("unsupported senseaudio audio model: %s", model),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, types.NewOpenAIError(readErr, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	switch kind {
	case audioKindMusic:
		return handleMusicResponse(c, info, body)
	case audioKindTTS:
		return handleTTSResponse(c, info, body)
	case audioKindSFX:
		return handleSFXResponse(c, info, body)
	default:
		return nil, types.NewOpenAIError(
			fmt.Errorf("unsupported senseaudio audio model: %s", model),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
		)
	}
}

// handleMusicResponse 走完异步链路：先取 create 响应的 task_id，再轮询 pending，
// 成功后把音频下载回来，按协议 10.5 以二进制返回。
func handleMusicResponse(c *gin.Context, info *relaycommon.RelayInfo, body []byte) (usage any, err *types.NewAPIError) {
	var created musicCreateResponse
	if unmarshalErr := common.Unmarshal(body, &created); unmarshalErr != nil {
		return nil, types.NewOpenAIError(
			fmt.Errorf("senseaudio music create decode failed: %w; body: %s", unmarshalErr, string(body)),
			types.ErrorCodeBadResponseBody,
			http.StatusBadGateway,
		)
	}
	taskId := strings.TrimSpace(created.TaskId)
	if taskId == "" {
		return nil, types.NewOpenAIError(
			fmt.Errorf("senseaudio music create response missing task_id: %s", string(body)),
			types.ErrorCodeBadResponseBody,
			http.StatusBadGateway,
		)
	}
	audioUrl, pollErr := pollMusicTask(c.Request.Context(), info, taskId)
	if pollErr != nil {
		return nil, types.NewOpenAIError(pollErr, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}
	return writeAudioFromUrl(c, info, audioUrl)
}

// handleTTSResponse 语音合成是同步的，音频以 hex 编码放在 data.audio。
func handleTTSResponse(c *gin.Context, info *relaycommon.RelayInfo, body []byte) (usage any, err *types.NewAPIError) {
	var payload ttsResponse
	if unmarshalErr := common.Unmarshal(body, &payload); unmarshalErr != nil {
		return nil, types.NewOpenAIError(
			fmt.Errorf("senseaudio tts decode failed: %w; body: %s", unmarshalErr, string(body)),
			types.ErrorCodeBadResponseBody,
			http.StatusBadGateway,
		)
	}
	if payload.BaseResp.StatusCode != 0 {
		return nil, types.NewOpenAIError(
			fmt.Errorf("senseaudio tts failed (%d): %s", payload.BaseResp.StatusCode, payload.BaseResp.StatusMsg),
			types.ErrorCodeBadResponseBody,
			http.StatusBadGateway,
		)
	}
	encoded := strings.TrimSpace(payload.Data.Audio)
	if encoded == "" {
		return nil, types.NewOpenAIError(
			fmt.Errorf("senseaudio tts response missing audio: %s", string(body)),
			types.ErrorCodeBadResponseBody,
			http.StatusBadGateway,
		)
	}
	// 上游文档写明是 hex；兼容一下 base64 以免不同版本行为不一致。
	audioBytes, decodeErr := hex.DecodeString(encoded)
	if decodeErr != nil {
		return nil, types.NewOpenAIError(
			fmt.Errorf("senseaudio tts audio hex decode failed: %w", decodeErr),
			types.ErrorCodeBadResponseBody,
			http.StatusBadGateway,
		)
	}
	c.Data(http.StatusOK, "audio/mpeg", audioBytes)
	return nil, nil
}

// handleSFXResponse 音效是同步批次接口，取第一个成功变体。
func handleSFXResponse(c *gin.Context, info *relaycommon.RelayInfo, body []byte) (usage any, err *types.NewAPIError) {
	var payload sfxResponse
	if unmarshalErr := common.Unmarshal(body, &payload); unmarshalErr != nil {
		return nil, types.NewOpenAIError(
			fmt.Errorf("senseaudio sfx decode failed: %w; body: %s", unmarshalErr, string(body)),
			types.ErrorCodeBadResponseBody,
			http.StatusBadGateway,
		)
	}
	var firstFailure string
	for _, item := range payload.Items {
		if url := strings.TrimSpace(item.AudioUrl); url != "" {
			return writeAudioFromUrl(c, info, url)
		}
		if firstFailure == "" {
			firstFailure = strings.TrimSpace(item.FailReason)
		}
	}
	if firstFailure == "" {
		firstFailure = "no audio variant returned"
	}
	return nil, types.NewOpenAIError(
		fmt.Errorf("senseaudio sfx failed: %s; body: %s", firstFailure, string(body)),
		types.ErrorCodeBadResponseBody,
		http.StatusBadGateway,
	)
}

// pollMusicTask 轮询到终态。上游是异步任务制，DoResponse 必须在这里同步等完，
// 否则拿不到音频地址。
func pollMusicTask(ctx context.Context, info *relaycommon.RelayInfo, taskId string) (string, error) {
	endpoint := strings.TrimRight(info.ChannelBaseUrl, "/") + pathMusicPending + taskId
	deadline := time.Now().Add(pollTimeout)
	for {
		result, done, err := fetchMusicTask(ctx, endpoint, info.ApiKey)
		if err != nil {
			return "", err
		}
		if done {
			return result, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("senseaudio music task %s timed out after %s", taskId, pollTimeout)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func fetchMusicTask(ctx context.Context, endpoint string, apiKey string) (audioUrl string, done bool, err error) {
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if reqErr != nil {
		return "", false, fmt.Errorf("build senseaudio pending request: %w", reqErr)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	res, doErr := http.DefaultClient.Do(req)
	if doErr != nil {
		return "", false, fmt.Errorf("query senseaudio music task: %w", doErr)
	}
	defer func() { _ = res.Body.Close() }()
	raw, readErr := io.ReadAll(res.Body)
	if readErr != nil {
		return "", false, fmt.Errorf("read senseaudio music task: %w", readErr)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", false, fmt.Errorf("senseaudio music task query failed with status %d: %s", res.StatusCode, string(raw))
	}
	var pending musicPendingResponse
	if unmarshalErr := common.Unmarshal(raw, &pending); unmarshalErr != nil {
		return "", false, fmt.Errorf("decode senseaudio music task: %w; body: %s", unmarshalErr, string(raw))
	}
	switch strings.ToUpper(strings.TrimSpace(pending.Status)) {
	case statusSuccess, statusCompleted:
		url := firstAudioUrl(pending)
		if url == "" {
			return "", false, fmt.Errorf("senseaudio music task succeeded without audio url: %s", string(raw))
		}
		return url, true, nil
	case statusFailed:
		reason := strings.TrimSpace(pending.FailReason)
		if reason == "" {
			reason = "unknown"
		}
		return "", false, fmt.Errorf("senseaudio music task failed: %s", reason)
	case statusPending, "":
		return "", false, nil
	default:
		return "", false, fmt.Errorf("senseaudio music task returned unknown status %q", pending.Status)
	}
}

func firstAudioUrl(pending musicPendingResponse) string {
	for _, item := range pending.Response.Data {
		if url := strings.TrimSpace(item.AudioUrl); url != "" {
			return url
		}
	}
	return ""
}

func writeAudioFromUrl(c *gin.Context, info *relaycommon.RelayInfo, audioUrl string) (usage any, err *types.NewAPIError) {
	audioBytes, contentType, downloadErr := downloadAudio(c.Request.Context(), audioUrl)
	if downloadErr != nil {
		return nil, types.NewOpenAIError(downloadErr, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}
	c.Data(http.StatusOK, contentType, audioBytes)
	return nil, nil
}

func downloadAudio(ctx context.Context, audioUrl string) ([]byte, string, error) {
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, audioUrl, nil)
	if reqErr != nil {
		return nil, "", fmt.Errorf("build senseaudio audio download: %w", reqErr)
	}
	res, doErr := http.DefaultClient.Do(req)
	if doErr != nil {
		return nil, "", fmt.Errorf("download senseaudio audio: %w", doErr)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download senseaudio audio failed with status %d", res.StatusCode)
	}
	audioBytes, readErr := io.ReadAll(res.Body)
	if readErr != nil {
		return nil, "", fmt.Errorf("read senseaudio audio: %w", readErr)
	}
	contentType := strings.TrimSpace(res.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "audio/mpeg"
	}
	return audioBytes, contentType, nil
}
