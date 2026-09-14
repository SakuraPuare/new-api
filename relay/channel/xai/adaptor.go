package xai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/QuantumNous/new-api/relay/constant"

	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

type Adaptor struct {
}

const maxImageEditBodyBytes = 8 << 20

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(*gin.Context, *relaycommon.RelayInfo, *dto.ClaudeRequest) (any, error) {
	//TODO implement me
	//panic("implement me")
	return nil, errors.New("not available")
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	//not available
	return nil, errors.New("not available")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	xaiRequest := ImageRequest{
		Model:  request.Model,
		Prompt: request.Prompt,
		N:      request.N,
	}
	if request.ResponseFormat != "" {
		xaiRequest.ResponseFormat = &request.ResponseFormat
	}
	if info.RelayMode != constant.RelayModeImagesEdits {
		return xaiRequest, nil
	}
	if c.Request.ContentLength > maxImageEditBodyBytes {
		return nil, fmt.Errorf("xAI image edit request exceeds 8 MiB: %w", common.ErrRequestBodyTooLarge)
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	if storage.Size() > maxImageEditBodyBytes {
		return nil, fmt.Errorf("xAI image edit request exceeds 8 MiB: %w", common.ErrRequestBodyTooLarge)
	}
	if len(request.Mask) > 0 && string(request.Mask) != "null" {
		return nil, errors.New("xAI image edits do not support a mask")
	}

	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil {
		return nil, fmt.Errorf("invalid image edit content type: %w", err)
	}
	if mediaType == "multipart/form-data" {
		form := c.Request.MultipartForm
		if form == nil {
			form, err = common.ParseMultipartFormReusable(c)
			if err != nil {
				return nil, fmt.Errorf("invalid image edit form: %w", err)
			}
		}
		if values := form.Value["response_format"]; len(values) > 0 {
			xaiRequest.ResponseFormat = &values[0]
		}
		if values := form.Value["aspect_ratio"]; len(values) > 0 {
			xaiRequest.AspectRatio = &values[0]
		}
		inputs, err := multipartImageInputs(c)
		if err != nil {
			return nil, err
		}
		if len(inputs) == 1 {
			xaiRequest.Image = &inputs[0]
		} else {
			xaiRequest.Images = inputs
		}
		return xaiRequest, nil
	}
	if mediaType == "application/json" {
		var options struct {
			AspectRatio    *string `json:"aspect_ratio"`
			ResponseFormat *string `json:"response_format"`
		}
		if err := common.UnmarshalBodyReusable(c, &options); err != nil {
			return nil, fmt.Errorf("invalid image edit JSON: %w", err)
		}
		xaiRequest.AspectRatio = options.AspectRatio
		if options.ResponseFormat != nil {
			xaiRequest.ResponseFormat = options.ResponseFormat
		}
	}

	if len(request.Image) > 0 && len(request.Images) > 0 {
		return nil, errors.New("provide image or images, not both")
	}
	if len(request.Image) > 0 {
		input, err := decodeImageInput(request.Image)
		if err != nil {
			return nil, err
		}
		xaiRequest.Image = &input
		return xaiRequest, nil
	}
	if len(request.Images) > 0 {
		var rawInputs []json.RawMessage
		if err := common.Unmarshal(request.Images, &rawInputs); err != nil {
			return nil, fmt.Errorf("invalid images: %w", err)
		}
		if len(rawInputs) == 0 || len(rawInputs) > 5 {
			return nil, errors.New("xAI image edits require 1 to 5 images")
		}
		for _, raw := range rawInputs {
			input, err := decodeImageInput(raw)
			if err != nil {
				return nil, err
			}
			xaiRequest.Images = append(xaiRequest.Images, input)
		}
		return xaiRequest, nil
	}
	return nil, errors.New("image is required")
}

func decodeImageInput(raw json.RawMessage) (ImageInput, error) {
	var url string
	if err := common.Unmarshal(raw, &url); err == nil {
		if strings.TrimSpace(url) == "" {
			return ImageInput{}, errors.New("image URL is required")
		}
		imageType := "image_url"
		return ImageInput{Type: &imageType, URL: &url}, nil
	}

	var input ImageInput
	if err := common.Unmarshal(raw, &input); err != nil {
		return ImageInput{}, fmt.Errorf("invalid image input: %w", err)
	}
	if (input.URL != nil && strings.TrimSpace(*input.URL) == "") ||
		(input.FileID != nil && strings.TrimSpace(*input.FileID) == "") ||
		(input.URL == nil) == (input.FileID == nil) {
		return ImageInput{}, errors.New("image input requires exactly one of url or file_id")
	}
	if input.URL != nil && input.Type != nil && *input.Type != "image_url" {
		return ImageInput{}, errors.New("invalid image URL type")
	}
	return input, nil
}

func multipartImageInputs(c *gin.Context) ([]ImageInput, error) {
	_, params, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || params["boundary"] == "" {
		return nil, errors.New("invalid multipart image edit boundary")
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	body, err := storage.NewReader()
	if err != nil {
		return nil, err
	}
	defer body.Close()

	reader := multipart.NewReader(body, params["boundary"])
	var inputs []ImageInput
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid image edit form: %w", err)
		}
		name := part.FormName()
		if name == "mask" && part.FileName() != "" {
			part.Close()
			return nil, errors.New("xAI image edits do not support a mask")
		}
		if name != "image" && name != "image[]" && name != "images" && name != "images[]" &&
			!(strings.HasPrefix(name, "image[") && strings.HasSuffix(name, "]")) {
			part.Close()
			continue
		}
		if len(inputs) == 5 {
			part.Close()
			return nil, errors.New("xAI image edits require 1 to 5 images")
		}

		if part.FileName() == "" {
			value, readErr := io.ReadAll(part)
			part.Close()
			if readErr != nil {
				return nil, fmt.Errorf("read image URL: %w", readErr)
			}
			text := strings.TrimSpace(string(value))
			if text == "" {
				return nil, errors.New("image URL is required")
			}
			if strings.HasPrefix(text, "{") || strings.HasPrefix(text, `"`) {
				input, err := decodeImageInput(json.RawMessage(text))
				if err != nil {
					return nil, err
				}
				inputs = append(inputs, input)
			} else {
				imageType := "image_url"
				inputs = append(inputs, ImageInput{Type: &imageType, URL: &text})
			}
			continue
		}

		var head [512]byte
		n, readErr := io.ReadFull(part, head[:])
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			part.Close()
			return nil, fmt.Errorf("read image file: %w", readErr)
		}
		contentType := http.DetectContentType(head[:n])
		if contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/webp" {
			part.Close()
			return nil, fmt.Errorf("unsupported image type: %s", contentType)
		}
		var encoded strings.Builder
		encoded.WriteString("data:")
		encoded.WriteString(contentType)
		encoded.WriteString(";base64,")
		encoder := base64.NewEncoder(base64.StdEncoding, &encoded)
		_, copyErr := io.Copy(encoder, io.MultiReader(bytes.NewReader(head[:n]), part))
		closeErr := encoder.Close()
		part.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("read image file: %w", copyErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("encode image file: %w", closeErr)
		}
		imageType := "image_url"
		imageURL := encoded.String()
		inputs = append(inputs, ImageInput{Type: &imageType, URL: &imageURL})
	}
	if len(inputs) == 0 {
		return nil, errors.New("image is required")
	}
	return inputs, nil
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return relaycommon.GetFullRequestURL(info.ChannelBaseUrl, info.RequestURLPath, info.ChannelType), nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	if info.RelayMode == constant.RelayModeImagesEdits {
		req.Set("Content-Type", "application/json")
	}
	req.Set("Authorization", "Bearer "+info.ApiKey)
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	if strings.HasSuffix(info.UpstreamModelName, "-search") {
		info.UpstreamModelName = strings.TrimSuffix(info.UpstreamModelName, "-search")
		request.Model = info.UpstreamModelName
		toMap := request.ToMap()
		toMap["search_parameters"] = map[string]any{
			"mode": "on",
		}
		return toMap, nil
	}
	if strings.HasPrefix(request.Model, "grok-3-mini") {
		if lo.FromPtrOr(request.MaxCompletionTokens, uint(0)) == 0 && lo.FromPtrOr(request.MaxTokens, uint(0)) != 0 {
			request.MaxCompletionTokens = request.MaxTokens
			request.MaxTokens = nil
		}
		preserveSuffix := model_setting.ShouldPreserveThinkingSuffix(info.OriginModelName) || model_setting.ShouldPreserveThinkingSuffix(request.Model)
		if !preserveSuffix && strings.HasSuffix(request.Model, "-high") {
			request.ReasoningEffort = "high"
			request.Model = strings.TrimSuffix(request.Model, "-high")
		} else if !preserveSuffix && strings.HasSuffix(request.Model, "-low") {
			request.ReasoningEffort = "low"
			request.Model = strings.TrimSuffix(request.Model, "-low")
		}
		info.SetReasoningEffort(request.ReasoningEffort)
		info.UpstreamModelName = request.Model
	}
	return request, nil
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	//not available
	return nil, errors.New("not available")
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	if request.Model == "" && info != nil {
		request.Model = info.UpstreamModelName
	}
	return request, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	switch info.RelayMode {
	case constant.RelayModeImagesEdits:
		usage, err = openai.OpenaiImageHandlerWithUsageHook(c, info, resp, func(parsed *dto.Usage) *types.NewAPIError {
			return applyImageEditNativeCost(info, parsed)
		})
	case constant.RelayModeImagesGenerations:
		usage, err = openai.OpenaiImageHandler(c, info, resp)
	case constant.RelayModeResponses:
		if info.IsStream {
			usage, err = openai.OaiResponsesStreamHandler(c, info, resp)
		} else {
			usage, err = openai.OaiResponsesHandler(c, info, resp)
		}
	default:
		if info.IsStream {
			usage, err = xAIStreamHandler(c, info, resp)
		} else {
			usage, err = xAIHandler(c, info, resp)
		}
	}
	return
}

func applyImageEditNativeCost(info *relaycommon.RelayInfo, usage *dto.Usage) *types.NewAPIError {
	if info.PriceData.UsePrice || info.TieredBillingSnapshot != nil || ratio_setting.HasConfiguredModelRatio(info.GetBillingModelName()) {
		return nil
	}
	if usage.CostInUSDTicks == nil || *usage.CostInUSDTicks < 0 {
		return types.NewErrorWithStatusCode(errors.New("xAI image edit response has no valid cost_in_usd_ticks"),
			types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}

	info.PriceData.ModelPrice = float64(*usage.CostInUSDTicks) / 10_000_000_000
	info.PriceData.UsePrice = true
	info.PriceData.ModelRatio = 0
	ratios := info.PriceData.OtherRatios()
	delete(ratios, "n")
	info.PriceData.ReplaceOtherRatios(ratios)
	return nil
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
