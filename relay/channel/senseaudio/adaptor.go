package senseaudio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// Adaptor 实现 SenseAudio 渠道。
//
// 上游各能力的协议差异全部在这里吸收，对 DramaClaw 暴露的仍是 DC-Media 统一
// 协议：音频走 /v1/audio/speech（音乐/TTS/音效按模型分流），图片走 images 语义。
// 与 fal 渠道同一套做法。
type Adaptor struct{}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	base := strings.TrimRight(info.ChannelBaseUrl, "/")
	switch info.RelayMode {
	case relayconstant.RelayModeAudioSpeech:
		_, path, ok := resolveAudioModel(musicModelName(info, dto.AudioRequest{}))
		if !ok {
			return "", fmt.Errorf("unsupported senseaudio audio model: %s", musicModelName(info, dto.AudioRequest{}))
		}
		return base + path, nil
	case relayconstant.RelayModeImagesGenerations:
		return base + pathImageSync, nil
	default:
		return "", fmt.Errorf("senseaudio: unsupported relay mode %d", info.RelayMode)
	}
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, header)
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json")
	header.Set("Authorization", "Bearer "+info.ApiKey)
	return nil
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	if info.RelayMode != relayconstant.RelayModeAudioSpeech {
		return nil, fmt.Errorf("senseaudio: unsupported audio relay mode %d", info.RelayMode)
	}
	if request.IsStream(c.Request) {
		return nil, fmt.Errorf("senseaudio audio does not support stream_format=%q", request.StreamFormat)
	}
	body, err := buildAudioRequest(musicModelName(info, request), request)
	if err != nil {
		return nil, err
	}
	return marshalBody(body)
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	if info.RelayMode != relayconstant.RelayModeImagesGenerations {
		return nil, fmt.Errorf("senseaudio: unsupported image relay mode %d", info.RelayMode)
	}
	return buildImageRequest(info.UpstreamModelName, request)
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	return nil, errors.New("senseaudio adaptor: ConvertOpenAIRequest is not implemented")
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, errors.New("senseaudio adaptor: ConvertRerankRequest is not implemented")
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("senseaudio adaptor: ConvertEmbeddingRequest is not implemented")
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return nil, errors.New("senseaudio adaptor: ConvertOpenAIResponsesRequest is not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	return nil, errors.New("senseaudio adaptor: ConvertClaudeRequest is not implemented")
}

func (a *Adaptor) ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	return nil, errors.New("senseaudio adaptor: ConvertGeminiRequest is not implemented")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	switch info.RelayMode {
	case relayconstant.RelayModeAudioSpeech:
		return handleAudioResponse(c, resp, info)
	case relayconstant.RelayModeImagesGenerations:
		return handleImageResponse(c, resp, info)
	default:
		return nil, types.NewOpenAIError(
			fmt.Errorf("senseaudio: unsupported relay mode %d", info.RelayMode),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
}

func (a *Adaptor) GetModelList() []string { return ModelList }

func (a *Adaptor) GetChannelName() string { return ChannelName }

func (a *Adaptor) GetCapabilities() []string { return []string{"image", "audio"} }

func marshalBody(body any) (io.Reader, error) {
	raw, err := common.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal senseaudio request: %w", err)
	}
	return bytes.NewReader(raw), nil
}

// musicModelName 取本次请求实际使用的模型：优先用渠道映射后的上游模型名，
// 与 fal 渠道一致。
func musicModelName(info *relaycommon.RelayInfo, request dto.AudioRequest) string {
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
