package elevenlabs

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// 三个能力共用 DC-Media 的 /v1/audio/speech 语义，按模型名分流到不同端点。
type audioKind int

const (
	audioKindMusic audioKind = iota
	audioKindTTS
	audioKindSoundEffect
)

func resolveAudioModel(model string) (audioKind, bool) {
	switch strings.TrimPrefix(strings.TrimSpace(model), "/") {
	case ModelMusic, "eleven-music":
		return audioKindMusic, true
	case ModelTTS, "eleven-tts":
		return audioKindTTS, true
	case ModelSoundEffect, "eleven-sfx", "eleven-sound-effect":
		return audioKindSoundEffect, true
	default:
		return 0, false
	}
}

type audioMetadata struct {
	MusicLengthMS            *int     `json:"music_length_ms,omitempty"`
	ForceInstrumental        *bool    `json:"force_instrumental,omitempty"`
	RespectSectionsDurations *bool    `json:"respect_sections_durations,omitempty"`
	OutputFormat             string   `json:"output_format,omitempty"`
	ModelId                  string   `json:"model_id,omitempty"`
	DurationSeconds          *float64 `json:"duration_seconds,omitempty"`
	PromptInfluence          *float64 `json:"prompt_influence,omitempty"`
	Stability                *float64 `json:"stability,omitempty"`
	SimilarityBoost          *float64 `json:"similarity_boost,omitempty"`
	Style                    *float64 `json:"style,omitempty"`
	Speed                    *float64 `json:"speed,omitempty"`
	LanguageCode             string   `json:"language_code,omitempty"`
}

type musicRequest struct {
	Prompt                   string `json:"prompt"`
	MusicLengthMS            int    `json:"music_length_ms"`
	ForceInstrumental        bool   `json:"force_instrumental"`
	RespectSectionsDurations bool   `json:"respect_sections_durations"`
	ModelId                  string `json:"model_id,omitempty"`
}

type ttsVoiceSettings struct {
	Stability       float64 `json:"stability"`
	SimilarityBoost float64 `json:"similarity_boost"`
	Style           float64 `json:"style"`
	UseSpeakerBoost bool    `json:"use_speaker_boost"`
	Speed           float64 `json:"speed"`
}

type ttsRequest struct {
	Text          string           `json:"text"`
	ModelId       string           `json:"model_id,omitempty"`
	VoiceSettings ttsVoiceSettings `json:"voice_settings"`
	LanguageCode  string           `json:"language_code,omitempty"`
}

type soundEffectRequest struct {
	Text            string   `json:"text"`
	PromptInfluence float64  `json:"prompt_influence"`
	DurationSeconds *float64 `json:"duration_seconds,omitempty"`
}

func parseAudioMetadata(request dto.AudioRequest) (audioMetadata, error) {
	var metadata audioMetadata
	if len(request.Metadata) == 0 {
		return metadata, nil
	}
	if err := common.Unmarshal(request.Metadata, &metadata); err != nil {
		return metadata, fmt.Errorf("invalid elevenlabs audio metadata: %w", err)
	}
	return metadata, nil
}

func derefFloat(value *float64, fallback float64) float64 {
	if value == nil {
		return fallback
	}
	return *value
}

func buildAudioRequest(kind audioKind, request dto.AudioRequest) (any, error) {
	input := strings.TrimSpace(request.Input)
	if input == "" {
		return nil, fmt.Errorf("input is required for elevenlabs audio")
	}
	metadata, err := parseAudioMetadata(request)
	if err != nil {
		return nil, err
	}
	modelId := strings.TrimSpace(metadata.ModelId)

	switch kind {
	case audioKindMusic:
		if metadata.MusicLengthMS != nil && (*metadata.MusicLengthMS < 3000 || *metadata.MusicLengthMS > 600000) {
			return nil, fmt.Errorf("metadata.music_length_ms must be between 3000 and 600000")
		}
		body := &musicRequest{
			Prompt:                   input,
			MusicLengthMS:            30000,
			ForceInstrumental:        true,
			RespectSectionsDurations: true,
		}
		if metadata.MusicLengthMS != nil {
			body.MusicLengthMS = *metadata.MusicLengthMS
		}
		if metadata.ForceInstrumental != nil {
			body.ForceInstrumental = *metadata.ForceInstrumental
		}
		if metadata.RespectSectionsDurations != nil {
			body.RespectSectionsDurations = *metadata.RespectSectionsDurations
		}
		body.ModelId = modelId
		if body.ModelId == "" {
			body.ModelId = DefaultMusicModelId
		}
		return body, nil

	case audioKindTTS:
		body := &ttsRequest{
			Text:    input,
			ModelId: modelId,
			VoiceSettings: ttsVoiceSettings{
				Stability:       derefFloat(metadata.Stability, 0.75),
				SimilarityBoost: derefFloat(metadata.SimilarityBoost, 0.85),
				Style:           derefFloat(metadata.Style, 0.0),
				UseSpeakerBoost: true,
				Speed:           derefFloat(metadata.Speed, 1.0),
			},
			LanguageCode: strings.TrimSpace(metadata.LanguageCode),
		}
		if body.ModelId == "" {
			body.ModelId = DefaultTTSModelId
		}
		return body, nil

	case audioKindSoundEffect:
		body := &soundEffectRequest{
			Text:            input,
			PromptInfluence: derefFloat(metadata.PromptInfluence, 0.3),
			DurationSeconds: metadata.DurationSeconds,
		}
		return body, nil

	default:
		return nil, fmt.Errorf("unsupported elevenlabs audio kind: %d", kind)
	}
}

// handleAudioResponse ElevenLabs 三个端点都直接返回音频二进制，无需二次解码
// 或下载（对比 senseaudio 的 hex / URL 形态）。
func handleAudioResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	defer func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()
	audioBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, types.NewOpenAIError(readErr, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	if len(audioBytes) == 0 {
		return nil, types.NewOpenAIError(
			fmt.Errorf("elevenlabs returned an empty audio body"),
			types.ErrorCodeBadResponseBody,
			http.StatusBadGateway,
		)
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "audio/mpeg"
	}
	c.Data(http.StatusOK, contentType, audioBytes)
	return nil, nil
}
