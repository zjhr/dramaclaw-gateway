package senseaudio

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	model "github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

// senseaudio 的视频接口：POST /v1/video/create 建任务，GET /v1/video/status?id=
// 轮询。与 doubao 渠道同为异步任务制，但字段形状不同——content 元素用扁平的
// url / audio_url / video_url，且状态枚举是 pending/processing/completed/failed。
type ContentItem struct {
	Type     string `json:"type,omitempty"`
	Text     string `json:"text,omitempty"`
	Url      string `json:"url,omitempty"`
	AudioUrl string `json:"audio_url,omitempty"`
	VideoUrl string `json:"video_url,omitempty"`
	Role     string `json:"role,omitempty"`
}

type requestPayload struct {
	Model      string        `json:"model"`
	Content    []ContentItem `json:"content,omitempty"`
	Duration   int           `json:"duration,omitempty"`
	Resolution string        `json:"resolution,omitempty"`
	Ratio      string        `json:"ratio,omitempty"`
	Timeout    int           `json:"timeout,omitempty"`
}

type createResponse struct {
	Id     string `json:"id"`
	TaskId string `json:"task_id"`
}

type statusResponse struct {
	Id           string `json:"id"`
	TaskId       string `json:"task_id"`
	Model        string `json:"model"`
	Status       string `json:"status"`
	VideoUrl     string `json:"video_url"`
	ErrorMessage string `json:"error_message"`
	CompletedAt  int64  `json:"completed_at"`
}

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = strings.TrimRight(info.ChannelBaseUrl, "/")
	a.apiKey = info.ApiKey
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *taskdto.TaskError {
	return relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
}

func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return a.baseURL + "/v1/video/create", nil
}

func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}
	body, err := convertToRequestPayload(&req)
	if err != nil {
		return nil, errors.Wrap(err, "convert request payload failed")
	}
	if info.IsModelMapped {
		body.Model = info.UpstreamModelName
	} else {
		info.UpstreamModelName = body.Model
	}
	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *taskdto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	var created createResponse
	if unmarshalErr := common.Unmarshal(responseBody, &created); unmarshalErr != nil {
		taskErr = service.TaskErrorWrapper(
			errors.Wrapf(unmarshalErr, "body: %s", responseBody),
			"unmarshal_response_body_failed",
			http.StatusInternalServerError,
		)
		return
	}
	upstreamID := strings.TrimSpace(created.Id)
	if upstreamID == "" {
		upstreamID = strings.TrimSpace(created.TaskId)
	}
	if upstreamID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName
	c.JSON(http.StatusOK, ov)

	return upstreamID, responseBody, nil
}

func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}
	uri := fmt.Sprintf("%s/v1/video/status?id=%s", strings.TrimRight(baseUrl, "/"), taskID)
	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) GetModelList() []string { return ModelList }

func (a *TaskAdaptor) GetChannelName() string { return ChannelName }

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	var task statusResponse
	if err := common.Unmarshal(respBody, &task); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}
	result := relaycommon.TaskInfo{Code: 0}
	switch strings.ToLower(strings.TrimSpace(task.Status)) {
	case "pending", "queued":
		result.Status = model.TaskStatusQueued
		result.Progress = "10%"
	case "processing", "running":
		result.Status = model.TaskStatusInProgress
		result.Progress = "50%"
	case "completed", "succeeded", "success":
		result.Status = model.TaskStatusSuccess
		result.Progress = "100%"
		result.Url = strings.TrimSpace(task.VideoUrl)
	case "failed":
		result.Status = model.TaskStatusFailure
		result.Progress = "100%"
		result.Reason = strings.TrimSpace(task.ErrorMessage)
	default:
		// 未知状态当作处理中，避免误判为失败而中断轮询。
		result.Status = model.TaskStatusInProgress
		result.Progress = "30%"
	}
	return &result, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var task statusResponse
	if err := common.Unmarshal(originTask.Data, &task); err != nil {
		return nil, errors.Wrap(err, "unmarshal senseaudio task data failed")
	}
	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.TaskID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	openAIVideo.SetMetadata("url", task.VideoUrl)
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt
	openAIVideo.Model = originTask.Properties.OriginModelName
	if strings.EqualFold(task.Status, "failed") {
		openAIVideo.Error = &dto.OpenAIVideoError{Message: task.ErrorMessage}
	}
	return common.Marshal(openAIVideo)
}

// convertToRequestPayload 从 DC-Media 的规范字段重建 content。
// 不解析、不透传供应商形状的 metadata.content——素材角色由规范字段表达。
func convertToRequestPayload(req *relaycommon.TaskSubmitReq) (*requestPayload, error) {
	r := requestPayload{
		Model:   req.Model,
		Content: []ContentItem{},
	}
	metadata := make(map[string]interface{}, len(req.Metadata))
	for key, value := range req.Metadata {
		if key != "content" {
			metadata[key] = value
		}
	}
	if err := taskcommon.UnmarshalMetadata(metadata, &r); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata failed")
	}
	dcMetadata := relaycommon.DCMediaMetadata{}
	if err := req.UnmarshalMetadata(&dcMetadata); err != nil {
		return nil, errors.Wrap(err, "unmarshal DC media metadata failed")
	}

	seen := map[string]bool{}
	appendMedia := func(kind, rawURL, role string) {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			return
		}
		identity := kind + "\x00" + role + "\x00" + rawURL
		if seen[identity] {
			return
		}
		seen[identity] = true
		item := ContentItem{Role: role}
		switch kind {
		case "image":
			item.Type, item.Url = "image", rawURL
		case "video":
			item.Type, item.VideoUrl = "video", rawURL
		case "audio":
			item.Type, item.AudioUrl = "audio", rawURL
		default:
			return
		}
		r.Content = append(r.Content, item)
	}
	appendMedia("image", req.Image, "first_frame")
	appendMedia("image", dcMetadata.LastFrameImage, "last_frame")
	for _, url := range dcMetadata.ReferenceImages {
		appendMedia("image", url, "reference_image")
	}
	for _, url := range dcMetadata.ReferenceVideos {
		appendMedia("video", url, "reference_video")
	}
	for _, url := range dcMetadata.ReferenceAudios {
		appendMedia("audio", url, "reference_audio")
	}
	// 文本永远放最后：上游按 content 顺序理解语义，提示词置于素材之后。
	r.Content = append(r.Content, ContentItem{Type: "text", Text: req.Prompt})
	return &r, nil
}
