package senseaudio

import (
	"encoding/json"
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

// 图片走同步接口 /v1/image/sync：一次请求直接拿 URL，比 async+pending 少一轮
// 轮询。上游没有批量参数，请求里的 n 只能为 1。
type imageSyncRequest struct {
	Model     string `json:"model"`
	Prompt    string `json:"prompt"`
	Reference string `json:"reference,omitempty"`
	Seed      *int   `json:"seed,omitempty"`
	Size      string `json:"size,omitempty"`
}

type imageSyncResponse struct {
	Url          string `json:"url"`
	ErrorMessage string `json:"error_message"`
}

func buildImageRequest(model string, request dto.ImageRequest) (any, error) {
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required for senseaudio image")
	}
	if request.N != nil && *request.N > 1 {
		return nil, fmt.Errorf("senseaudio image does not support n > 1")
	}
	normalized := strings.TrimSpace(model)
	if normalized == "" {
		normalized = strings.TrimSpace(request.Model)
	}
	if _, ok := imageModelSet[normalized]; !ok {
		return nil, fmt.Errorf("unsupported senseaudio image model: %s", normalized)
	}
	body := &imageSyncRequest{
		Model:  normalized,
		Prompt: prompt,
		Size:   normalizeImageSize(request),
	}
	if ref := readImageReference(request); ref != "" {
		body.Reference = ref
	}
	if seed := readImageSeed(request); seed != nil {
		body.Seed = seed
	}
	return body, nil
}

var imageModelSet = map[string]struct{}{
	ModelImage20:       {},
	ModelImageSeedream: {},
	ModelImageNovaFast: {},
}

// normalizeImageSize 用网关已归一化的几何值；缺失时不发，交给上游默认。
func normalizeImageSize(request dto.ImageRequest) string {
	if request.Width > 0 && request.Height > 0 {
		return fmt.Sprintf("%dx%d", request.Width, request.Height)
	}
	size := strings.TrimSpace(request.Size)
	if size == "" || strings.EqualFold(size, "auto") {
		return ""
	}
	return size
}

// readImageReference 从 DC-Media 的 metadata 或 OpenAI 的 images 字段里取参考图。
func readImageReference(request dto.ImageRequest) string {
	if len(request.Metadata) > 0 {
		if raw, ok := request.Metadata["reference"]; ok {
			if text, isText := raw.(string); isText {
				if trimmed := strings.TrimSpace(text); trimmed != "" {
					return trimmed
				}
			}
		}
	}
	if len(request.Images) > 0 {
		var list []string
		if err := json.Unmarshal(request.Images, &list); err == nil && len(list) > 0 {
			return strings.TrimSpace(list[0])
		}
		var single string
		if err := json.Unmarshal(request.Images, &single); err == nil {
			return strings.TrimSpace(single)
		}
	}
	return ""
}

func readImageSeed(request dto.ImageRequest) *int {
	if len(request.Metadata) == 0 {
		return nil
	}
	raw, ok := request.Metadata["seed"]
	if !ok {
		return nil
	}
	switch value := raw.(type) {
	case float64:
		seed := int(value)
		return &seed
	case int:
		seed := value
		return &seed
	default:
		return nil
	}
}

// handleImageResponse 把上游的单个 URL 转成协议 11.1 的 data 数组形态。
func handleImageResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	defer func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, types.NewOpenAIError(readErr, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	var payload imageSyncResponse
	if unmarshalErr := common.Unmarshal(body, &payload); unmarshalErr != nil {
		return nil, types.NewOpenAIError(
			fmt.Errorf("senseaudio image decode failed: %w; body: %s", unmarshalErr, string(body)),
			types.ErrorCodeBadResponseBody,
			http.StatusBadGateway,
		)
	}
	url := strings.TrimSpace(payload.Url)
	if url == "" {
		reason := strings.TrimSpace(payload.ErrorMessage)
		if reason == "" {
			reason = "response missing url"
		}
		return nil, types.NewOpenAIError(
			fmt.Errorf("senseaudio image failed: %s; body: %s", reason, string(body)),
			types.ErrorCodeBadResponseBody,
			http.StatusBadGateway,
		)
	}
	c.JSON(http.StatusOK, gin.H{
		"created": time.Now().Unix(),
		"data":    []gin.H{{"url": url}},
	})
	return nil, nil
}
