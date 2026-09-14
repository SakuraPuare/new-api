package xai

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type editImageFile struct {
	field    string
	filename string
	content  []byte
}

func newImageRelayInfo(mode int) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayMode:   mode,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
}

func newEditMultipartContext(t *testing.T, files ...editImageFile) *gin.Context {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "client-image-alias"))
	require.NoError(t, writer.WriteField("prompt", "edit this image"))
	require.NoError(t, writer.WriteField("n", "1"))
	for _, file := range files {
		part, err := writer.CreateFormFile(file.field, file.filename)
		require.NoError(t, err)
		_, err = part.Write(file.content)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	return c
}

func testImageBytes(t *testing.T, format string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var body bytes.Buffer
	switch format {
	case "jpeg":
		require.NoError(t, jpeg.Encode(&body, img, nil))
	case "png":
		require.NoError(t, png.Encode(&body, img))
	default:
		t.Fatalf("unexpected test format: %s", format)
	}
	return body.Bytes()
}

func TestConvertImageEditMultipartForXAI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jpegBytes := testImageBytes(t, "jpeg")
	pngBytes := testImageBytes(t, "png")
	jpegURI := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpegBytes)
	pngURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)

	tests := []struct {
		name  string
		files []editImageFile
		want  []string
	}{
		{
			name:  "one image",
			files: []editImageFile{{field: "image", filename: "input.jpg", content: jpegBytes}},
			want:  []string{jpegURI},
		},
		{
			name: "ordered image array",
			files: []editImageFile{
				{field: "image[]", filename: "first.jpg", content: jpegBytes},
				{field: "image[]", filename: "second.png", content: pngBytes},
			},
			want: []string{jpegURI, pngURI},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newEditMultipartContext(t, tt.files...)
			info := newImageRelayInfo(relayconstant.RelayModeImagesEdits)
			converted, err := (&Adaptor{}).ConvertImageRequest(c, info, dto.ImageRequest{
				Model: "grok-imagine-image-quality", Prompt: "edit this image", N: common.GetPointer(uint(1)),
			})
			require.NoError(t, err)

			body, err := common.Marshal(converted)
			require.NoError(t, err)
			var upstream map[string]any
			require.NoError(t, common.Unmarshal(body, &upstream))
			assert.Equal(t, "grok-imagine-image-quality", upstream["model"])
			assert.Equal(t, "edit this image", upstream["prompt"])
			if len(tt.want) == 1 {
				assert.Equal(t, map[string]any{"type": "image_url", "url": tt.want[0]}, upstream["image"])
				assert.NotContains(t, upstream, "images")
			} else {
				assert.NotContains(t, upstream, "image")
				assert.Equal(t, []any{
					map[string]any{"type": "image_url", "url": tt.want[0]},
					map[string]any{"type": "image_url", "url": tt.want[1]},
				}, upstream["images"])
			}

			headers := make(http.Header)
			require.NoError(t, (&Adaptor{}).SetupRequestHeader(c, &headers, info))
			assert.Equal(t, "application/json", headers.Get("Content-Type"))
		})
	}
}

func TestImageEditOutboundRequestForXAI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jpegBytes := testImageBytes(t, "jpeg")
	requestSeen := make(chan struct {
		contentType string
		path        string
		body        []byte
		err         error
	}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		requestSeen <- struct {
			contentType string
			path        string
			body        []byte
			err         error
		}{r.Header.Get("Content-Type"), r.URL.Path, body, err}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	c := newEditMultipartContext(t, editImageFile{field: "image", filename: "input.jpg", content: jpegBytes})
	info := newImageRelayInfo(relayconstant.RelayModeImagesEdits)
	info.ChannelBaseUrl = server.URL
	info.RequestURLPath = "/v1/images/edits"
	info.ApiKey = "test-key"
	adaptor := &Adaptor{}
	converted, err := adaptor.ConvertImageRequest(c, info, dto.ImageRequest{Model: "grok-imagine-image-quality", Prompt: "edit this image"})
	require.NoError(t, err)
	requestBody, err := common.Marshal(converted)
	require.NoError(t, err)
	response, err := adaptor.DoRequest(c, info, bytes.NewReader(requestBody))
	require.NoError(t, err)
	require.NotNil(t, response)
	httpResponse, ok := response.(*http.Response)
	require.True(t, ok)
	defer httpResponse.Body.Close()
	assert.Equal(t, http.StatusOK, httpResponse.StatusCode)

	seen := <-requestSeen
	require.NoError(t, seen.err)
	assert.Equal(t, "application/json", seen.contentType)
	assert.Equal(t, "/v1/images/edits", seen.path)
	var upstream map[string]any
	require.NoError(t, common.Unmarshal(seen.body, &upstream))
	assert.Equal(t, "grok-imagine-image-quality", upstream["model"])
	assert.Equal(t, map[string]any{
		"type": "image_url",
		"url":  "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpegBytes),
	}, upstream["image"])
}

func TestConvertImageEditJSONReferencesForXAI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		image      string
		images     string
		wantImage  map[string]any
		wantImages []any
	}{
		{
			name:      "public URL",
			image:     `{"url":"https://example.com/input.jpg","type":"image_url"}`,
			wantImage: map[string]any{"url": "https://example.com/input.jpg", "type": "image_url"},
		},
		{
			name:      "file ID",
			image:     `{"file_id":"file_123"}`,
			wantImage: map[string]any{"file_id": "file_123"},
		},
		{
			name:       "mixed references",
			images:     `[{"url":"https://example.com/input.jpg","type":"image_url"},{"file_id":"file_123"}]`,
			wantImages: []any{map[string]any{"url": "https://example.com/input.jpg", "type": "image_url"}, map[string]any{"file_id": "file_123"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewBufferString("{}"))
			c.Request.Header.Set("Content-Type", "application/json")
			info := newImageRelayInfo(relayconstant.RelayModeImagesEdits)
			converted, err := (&Adaptor{}).ConvertImageRequest(c, info, dto.ImageRequest{
				Model: "grok-imagine-image-quality", Prompt: "edit this image", Image: []byte(tt.image), Images: []byte(tt.images),
			})
			require.NoError(t, err)
			body, err := common.Marshal(converted)
			require.NoError(t, err)
			var upstream map[string]any
			require.NoError(t, common.Unmarshal(body, &upstream))
			assert.Equal(t, "grok-imagine-image-quality", upstream["model"])
			assert.Equal(t, "edit this image", upstream["prompt"])
			if tt.wantImage != nil {
				assert.Equal(t, tt.wantImage, upstream["image"])
				assert.NotContains(t, upstream, "images")
			} else {
				assert.NotContains(t, upstream, "image")
				assert.Equal(t, tt.wantImages, upstream["images"])
			}
		})
	}
}

func TestConvertImageEditRejectsInvalidSourcesForXAI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jpegBytes := testImageBytes(t, "jpeg")
	tests := []struct {
		name  string
		files []editImageFile
	}{
		{name: "missing image"},
		{name: "unsupported format", files: []editImageFile{{field: "image", filename: "input.gif", content: []byte("GIF89a")}}},
		{name: "more than five images", files: []editImageFile{
			{field: "image[]", filename: "1.jpg", content: jpegBytes},
			{field: "image[]", filename: "2.jpg", content: jpegBytes},
			{field: "image[]", filename: "3.jpg", content: jpegBytes},
			{field: "image[]", filename: "4.jpg", content: jpegBytes},
			{field: "image[]", filename: "5.jpg", content: jpegBytes},
			{field: "image[]", filename: "6.jpg", content: jpegBytes},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newEditMultipartContext(t, tt.files...)
			info := newImageRelayInfo(relayconstant.RelayModeImagesEdits)
			_, err := (&Adaptor{}).ConvertImageRequest(c, info, dto.ImageRequest{Model: "grok-imagine-image-quality", Prompt: "edit this image"})
			require.Error(t, err)
		})
	}

	t.Run("malformed multipart", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewBufferString("--bad-boundary\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\ngrok-imagine-image-quality"))
		c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=bad-boundary")
		info := newImageRelayInfo(relayconstant.RelayModeImagesEdits)
		_, err := (&Adaptor{}).ConvertImageRequest(c, info, dto.ImageRequest{Model: "grok-imagine-image-quality", Prompt: "edit this image"})
		require.Error(t, err)
	})
}

func TestConvertImageGenerationForXAI(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewBufferString(`{"model":"grok-imagine-image-quality","prompt":"a tree"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	info := newImageRelayInfo(relayconstant.RelayModeImagesGenerations)
	converted, err := (&Adaptor{}).ConvertImageRequest(c, info, dto.ImageRequest{
		Model: "grok-imagine-image-quality", Prompt: "a tree", N: common.GetPointer(uint(2)), ResponseFormat: "b64_json",
	})
	require.NoError(t, err)
	body, err := common.Marshal(converted)
	require.NoError(t, err)
	var upstream map[string]any
	require.NoError(t, common.Unmarshal(body, &upstream))
	assert.Equal(t, "grok-imagine-image-quality", upstream["model"])
	assert.Equal(t, "a tree", upstream["prompt"])
	assert.Equal(t, float64(2), upstream["n"])
	assert.Equal(t, "b64_json", upstream["response_format"])
	assert.NotContains(t, upstream, "image")
	assert.NotContains(t, upstream, "images")

	headers := make(http.Header)
	require.NoError(t, (&Adaptor{}).SetupRequestHeader(c, &headers, info))
	assert.Equal(t, "application/json", headers.Get("Content-Type"))
}
