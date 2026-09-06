package plugins_test

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/relay/channel"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestXAIResponsesProtocol(t *testing.T) {
	testVideoResponsesProtocol(t, videoResponsesTestCase{
		pluginKey: "xai",
		model:     "grok-imagine-video-1.5",
		requestBody: map[string]any{
			"model": "grok-imagine-video-1.5", "input": "A paper boat drifting down a rain-soaked street",
			"duration": 10, "aspect_ratio": "16:9", "resolution": "720p",
		},
		wantAction: "text_to_video",
		wantRequest: map[string]any{
			"model": "grok-imagine-video-1.5", "prompt": "A paper boat drifting down a rain-soaked street",
			"duration": float64(10), "aspect_ratio": "16:9", "resolution": "720p",
		},
		wantUsageKeys: []string{"duration", "aspect_ratio", "resolution"}, wantVendorName: "xai",
	})
}

func loadXAIPlugin(t *testing.T) *jsplugin.LoadedPlugin {
	t.Helper()
	source, err := builtinplugins.Source("xai")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "xai"})
	require.NoError(t, err)
	return plugin
}

func callXAIHook(t *testing.T, plugin *jsplugin.LoadedPlugin, hook string, args ...any) map[string]any {
	t.Helper()
	value, err := plugin.Engine.Call(t.Context(), hook, args...)
	require.NoError(t, err)
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(encoded, &decoded))
	return decoded
}

func TestXAISubmitAndPollingHooks(t *testing.T) {
	plugin := loadXAIPlugin(t)
	ctx := map[string]any{"requestBody": map[string]any{
		"prompt": "a fox running through snow", "duration": 5, "aspect_ratio": "16:9", "resolution": "720p",
	}, "model": "grok-imagine-video-1.5", "upstreamModel": "grok-imagine-video-1.5", "baseUrl": "https://api.x.ai", "apiKey": "test-key"}
	submit := callXAIHook(t, plugin, "buildSubmitRequest", ctx)
	assert.Equal(t, "https://api.x.ai/v1/videos/generations", submit["url"])
	assert.Equal(t, "text_to_video", submit["action"])
	body, err := common.Marshal(submit["body"])
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"grok-imagine-video-1.5","prompt":"a fox running through snow","duration":5,"aspect_ratio":"16:9","resolution":"720p"}`, string(body))
	parsed := callXAIHook(t, plugin, "parseSubmitResponse", map[string]any{}, map[string]any{"body": map[string]any{"request_id": "req-123", "status": "pending"}})
	assert.Equal(t, "req-123", parsed["taskId"])
	assert.Equal(t, "IN_PROGRESS", callXAIHook(t, plugin, "parseTaskResult", map[string]any{}, map[string]any{"status": "pending"})["status"])
	done := callXAIHook(t, plugin, "parseTaskResult", map[string]any{}, map[string]any{"status": "done", "video": map[string]any{"url": "https://cdn.example/video.mp4"}})
	assert.Equal(t, "SUCCESS", done["status"])
	assert.Equal(t, "https://cdn.example/video.mp4", done["url"])
	failed := callXAIHook(t, plugin, "parseTaskResult", map[string]any{}, map[string]any{"status": "failed", "error": map[string]any{"message": "content policy"}})
	assert.Equal(t, "FAILURE", failed["status"])
}

func TestXAIRejectsInvalidDurationAndProxiesVideo(t *testing.T) {
	plugin := loadXAIPlugin(t)
	_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"requestBody": map[string]any{"prompt": "p", "duration": 30}, "model": "grok-imagine-video", "baseUrl": "https://api.x.ai", "apiKey": "k"})
	require.ErrorContains(t, err, "duration must be an integer between 1 and 15 seconds")
	adaptor := taskplugin.New(plugin)
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "test-key", ChannelBaseUrl: "https://api.x.ai"}})
	data, err := common.Marshal(map[string]any{"video": map[string]any{"url": "https://cdn.example/video.mp4"}})
	require.NoError(t, err)
	task := &model.Task{TaskID: "req-123", Status: model.TaskStatusSuccess, Data: data}
	artifacts, err := adaptor.ListArtifacts(task)
	require.NoError(t, err)
	assert.Equal(t, []channel.TaskArtifact{{Key: "video", Type: "video", MimeType: "video/mp4"}}, artifacts)
	descriptor, err := adaptor.BuildContentRequest(task, "video", channel.TaskArtifactClientRequest{Method: http.MethodGet})
	require.NoError(t, err)
	assert.Equal(t, "https://cdn.example/video.mp4", descriptor.URL)
	assert.Equal(t, http.MethodGet, descriptor.Method)
	assert.True(t, descriptor.Credentialless)
}
