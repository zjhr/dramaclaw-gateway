package elevenlabs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// Adaptor 实现 ElevenLabs 渠道。
//
// 覆盖音乐、语音合成与音效。音色克隆**不在**这里：它要上传参考音频（multipart）
// 且需要项目侧按样本哈希缓存，网关作为无状态转发层不适合承接。
type Adaptor struct {
	// TTS 的 voice_id 与音乐的 output_format 都只存在于请求体，而
	// GetRequestURL 拿不到请求体。ConvertAudioRequest 先于 DoRequest 执行，
	// 所以把解析结果缓存在实例上——GetAdaptor 每次请求返回新实例，不会串值。
	ttsVoiceId   string
	outputFormat string
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	base := strings.TrimRight(info.ChannelBaseUrl, "/")
	kind, ok := resolveAudioModel(audioModelName(info, dto.AudioRequest{}))
	if !ok {
		return "", fmt.Errorf("unsupported elevenlabs audio model: %s", audioModelName(info, dto.AudioRequest{}))
	}
	switch kind {
	case audioKindMusic:
		endpoint := base + pathMusic
		if format := strings.TrimSpace(a.outputFormat); format != "" {
			endpoint += "?output_format=" + url.QueryEscape(format)
		}
		return endpoint, nil
	case audioKindTTS:
		if a.ttsVoiceId == "" {
			return "", errors.New("elevenlabs tts requires a voice id")
		}
		return base + pathTextToSpeech + url.PathEscape(a.ttsVoiceId), nil
	case audioKindSoundEffect:
		return base + pathSoundGenerate, nil
	default:
		return "", fmt.Errorf("unsupported elevenlabs audio kind: %d", kind)
	}
}

// SetupRequestHeader 用 xi-api-key，而不是 Authorization: Bearer。
func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, header)
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "audio/*")
	header.Set("xi-api-key", info.ApiKey)
	return nil
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	if info.RelayMode != relayconstant.RelayModeAudioSpeech {
		return nil, fmt.Errorf("elevenlabs: unsupported audio relay mode %d", info.RelayMode)
	}
	if request.IsStream(c.Request) {
		return nil, fmt.Errorf("elevenlabs audio does not support stream_format=%q", request.StreamFormat)
	}
	model := audioModelName(info, request)
	kind, ok := resolveAudioModel(model)
	if !ok {
		return nil, fmt.Errorf("unsupported elevenlabs audio model: %s", model)
	}
	// 供 GetRequestURL 使用：这两项只存在于请求体里。
	a.ttsVoiceId = strings.TrimSpace(request.Voice)
	a.outputFormat = strings.TrimSpace(request.ResponseFormat)
	if kind == audioKindMusic && a.outputFormat == "" {
		a.outputFormat = defaultMusicOutputFormat
	}
	body, err := buildAudioRequest(kind, request)
	if err != nil {
		return nil, err
	}
	raw, err := common.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal elevenlabs request: %w", err)
	}
	return bytes.NewReader(raw), nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	return nil, errors.New("elevenlabs adaptor: ConvertOpenAIRequest is not implemented")
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, errors.New("elevenlabs adaptor: ConvertRerankRequest is not implemented")
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("elevenlabs adaptor: ConvertEmbeddingRequest is not implemented")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	return nil, errors.New("elevenlabs adaptor: ConvertImageRequest is not implemented")
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return nil, errors.New("elevenlabs adaptor: ConvertOpenAIResponsesRequest is not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	return nil, errors.New("elevenlabs adaptor: ConvertClaudeRequest is not implemented")
}

func (a *Adaptor) ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	return nil, errors.New("elevenlabs adaptor: ConvertGeminiRequest is not implemented")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	if info.RelayMode != relayconstant.RelayModeAudioSpeech {
		return nil, types.NewOpenAIError(
			fmt.Errorf("elevenlabs: unsupported relay mode %d", info.RelayMode),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	return handleAudioResponse(c, resp, info)
}

func (a *Adaptor) GetModelList() []string { return ModelList }

func (a *Adaptor) GetChannelName() string { return ChannelName }

func (a *Adaptor) GetCapabilities() []string { return []string{"audio"} }

// audioModelName 取本次请求实际使用的模型：优先用渠道映射后的上游模型名。
func audioModelName(info *relaycommon.RelayInfo, request dto.AudioRequest) string {
	if info != nil {
		if info.ChannelMeta != nil && strings.TrimSpace(info.UpstreamModelName) != "" {
			return strings.TrimSpace(info.UpstreamModelName)
		}
		if strings.TrimSpace(info.OriginModelName) != "" {
			return strings.TrimSpace(info.OriginModelName)
		}
	}
	return strings.TrimSpace(request.Model)
}
