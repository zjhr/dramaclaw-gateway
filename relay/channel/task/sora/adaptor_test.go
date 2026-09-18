package sora

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSoraBuildRequestBodyReturnsReplayablePassThroughBody(t *testing.T) {
	payload := []byte("opaque-sora-request-body")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/octet-stream")
	defer common.CleanupBodyStorage(c)

	info := &relaycommon.RelayInfo{}
	body, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
	require.NoError(t, err)
	replayable, ok := body.(common.ReplayableBody)
	require.True(t, ok)

	sent, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, payload, sent)
	assert.EqualValues(t, len(payload), replayable.Size())

	replayBody, err := replayable.NewReader()
	require.NoError(t, err)
	replay, err := io.ReadAll(replayBody)
	require.NoError(t, err)
	require.NoError(t, replayBody.Close())
	assert.Equal(t, payload, replay)
}

func TestNormalizeOpenAIVideoBody(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]interface{}
		want map[string]interface{}
	}{
		{
			name: "时长/画幅/分辨率翻译成上游字段并删除原名",
			in: map[string]interface{}{
				"prompt":     "x",
				"duration":   float64(8),
				"ratio":      "9:16",
				"resolution": "720p",
			},
			want: map[string]interface{}{
				"prompt":       "x",
				"seconds":      "8",
				"aspect_ratio": "9:16",
				"size":         "720P",
			},
		},
		{
			name: "上游已给目标字段时不覆盖，原名仍删除",
			in: map[string]interface{}{
				"duration": float64(8),
				"seconds":  "12",
				"ratio":    "1:1",
				"size":     "1080P",
			},
			want: map[string]interface{}{
				"seconds":      "12",
				"aspect_ratio": "1:1",
				"size":         "1080P",
			},
		},
		{
			name: "非 720p 分辨率只做透传，交给上游报错",
			in:   map[string]interface{}{"resolution": "1080p"},
			want: map[string]interface{}{"size": "1080p"},
		},
		{
			name: "auto 时长原样透传",
			in:   map[string]interface{}{"duration": "auto"},
			want: map[string]interface{}{"seconds": "auto"},
		},
		{
			name: "非整数时长保留小数",
			in:   map[string]interface{}{"duration": 7.5},
			want: map[string]interface{}{"seconds": "7.5"},
		},
		{
			name: "画幅藏在 metadata.ratio 时也能取出，且 metadata 被清理",
			in: map[string]interface{}{
				"prompt":   "x",
				"duration": float64(8),
				"metadata": map[string]interface{}{"ratio": "9:16", "resolution": "720p"},
			},
			want: map[string]interface{}{
				"prompt":       "x",
				"seconds":      "8",
				"aspect_ratio": "9:16",
			},
		},
		{
			name: "顶层 ratio 优先于 metadata.ratio",
			in: map[string]interface{}{
				"ratio":    "4:3",
				"metadata": map[string]interface{}{"ratio": "9:16"},
			},
			want: map[string]interface{}{"aspect_ratio": "4:3"},
		},
		{
			name: "width/height 是 NewAPI 内部表示，一并清理",
			in: map[string]interface{}{
				"width":  float64(720),
				"height": float64(1280),
			},
			want: map[string]interface{}{},
		},
		{
			name: "metadata.ratio 是空白串时不当作画幅",
			in: map[string]interface{}{
				"metadata": map[string]interface{}{"ratio": "   "},
			},
			want: map[string]interface{}{},
		},
		{
			name: "没有相关字段时不动请求体",
			in:   map[string]interface{}{"prompt": "x", "mode": "text"},
			want: map[string]interface{}{"prompt": "x", "mode": "text"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			normalizeOpenAIVideoBody(tc.in)
			assert.Equal(t, tc.want, tc.in)
		})
	}
}
