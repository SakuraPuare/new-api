package plugins_test

import "testing"

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
