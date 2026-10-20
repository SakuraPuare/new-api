package gemini

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newGeminiStreamTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	t.Cleanup(func() {
		constant.StreamingTimeout = oldStreamingTimeout
	})
	return c, recorder
}

func geminiStreamResponse(t *testing.T, frames ...dto.GeminiChatResponse) *http.Response {
	t.Helper()
	var body strings.Builder
	for _, frame := range frames {
		raw, err := common.Marshal(frame)
		require.NoError(t, err)
		body.WriteString("data: ")
		body.Write(raw)
		body.WriteByte('\n')
	}
	body.WriteString("data: [DONE]\n")
	return &http.Response{Body: io.NopCloser(strings.NewReader(body.String()))}
}

func geminiTestRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "gemini-3.8-flash",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3.8-flash-high",
		},
	}
}

// Gemini answers some turns with candidate frames that carry no parts at all,
// e.g. MALFORMED_FUNCTION_CALL or OTHER. Those turns must surface as an empty
// upstream error (retryable) instead of a 200 stream with no finish_reason,
// which OpenAI-compatible clients report as a truncated stream.
func TestGeminiChatStreamHandlerEmptyTurnFailsInsteadOfStreamingNothing(t *testing.T) {
	c, recorder := newGeminiStreamTestContext(t)
	info := geminiTestRelayInfo()

	resp := geminiStreamResponse(t,
		dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{
				Content: dto.GeminiChatContent{Role: "model"},
			}},
		},
		dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{
				Content:      dto.GeminiChatContent{Role: "model"},
				FinishReason: common.GetPointer("MALFORMED_FUNCTION_CALL"),
			}},
			UsageMetadata: dto.GeminiUsageMetadata{
				PromptTokenCount: 12752,
				TotalTokenCount:  12752,
			},
		},
	)

	usage, newAPIError := GeminiChatStreamHandler(c, info, resp)

	require.NotNil(t, newAPIError)
	assert.Equal(t, http.StatusInternalServerError, newAPIError.StatusCode)
	assert.Equal(t, string(types.ErrorCodeEmptyResponse), string(newAPIError.GetErrorCode()))
	assert.False(t, types.IsSkipRetryError(newAPIError), "the first empty attempt must stay retryable")
	require.NotNil(t, usage)
	assert.Equal(t, 0, usage.CompletionTokens)
	assert.Empty(t, recorder.Body.String(), "nothing may be flushed before the retry decision")
}

func TestGeminiChatStreamHandlerEmptyTurnStopsRetryingAfterBudget(t *testing.T) {
	c, recorder := newGeminiStreamTestContext(t)
	info := geminiTestRelayInfo()
	info.RetryIndex = maxGeminiEmptyResponseRetries

	resp := geminiStreamResponse(t, dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{{
			Content:      dto.GeminiChatContent{Role: "model"},
			FinishReason: common.GetPointer("OTHER"),
		}},
	})

	_, newAPIError := GeminiChatStreamHandler(c, info, resp)

	require.NotNil(t, newAPIError)
	assert.True(t, types.IsSkipRetryError(newAPIError), "empty turns must not loop upstream forever")
	assert.Empty(t, recorder.Body.String())
}

// A stream that delivers content but never a mapped finish reason must still be
// closed with an explicit one, otherwise clients reject the finished stream.
func TestGeminiChatStreamHandlerClosesStreamWithoutUpstreamFinishReason(t *testing.T) {
	c, recorder := newGeminiStreamTestContext(t)
	info := geminiTestRelayInfo()

	resp := geminiStreamResponse(t, dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{{
			Content: dto.GeminiChatContent{
				Role:  "model",
				Parts: []dto.GeminiPart{{Text: "hello"}},
			},
		}},
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:     10,
			CandidatesTokenCount: 2,
			TotalTokenCount:      12,
		},
	})

	usage, newAPIError := GeminiChatStreamHandler(c, info, resp)
	require.Nil(t, newAPIError)
	require.NotNil(t, usage)

	var finishReasons []string
	for line := range strings.SplitSeq(recorder.Body.String(), "\n") {
		payload, ok := strings.CutPrefix(strings.TrimSpace(line), "data: ")
		if !ok || payload == "[DONE]" {
			continue
		}
		var chunk dto.ChatCompletionsStreamResponse
		require.NoError(t, common.UnmarshalJsonStr(payload, &chunk))
		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil {
				finishReasons = append(finishReasons, *choice.FinishReason)
			}
		}
	}
	assert.Equal(t, []string{types.FinishReasonStop}, finishReasons)
	assert.True(t, bytes.Contains(recorder.Body.Bytes(), []byte("data: [DONE]")))
}

// MALFORMED_FUNCTION_CALL used to vanish from the finish_reason mapping, so a
// content turn that ended with it looked truncated to the client.
func TestGeminiChatStreamHandlerMapsUnknownFinishReasonToStop(t *testing.T) {
	c, recorder := newGeminiStreamTestContext(t)
	info := geminiTestRelayInfo()

	resp := geminiStreamResponse(t, dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{{
			Content: dto.GeminiChatContent{
				Role:  "model",
				Parts: []dto.GeminiPart{{Text: "partial answer"}},
			},
			FinishReason: common.GetPointer("MALFORMED_FUNCTION_CALL"),
		}},
	})

	_, newAPIError := GeminiChatStreamHandler(c, info, resp)
	require.Nil(t, newAPIError)

	var finishReasons []string
	for line := range strings.SplitSeq(recorder.Body.String(), "\n") {
		payload, ok := strings.CutPrefix(strings.TrimSpace(line), "data: ")
		if !ok || payload == "[DONE]" {
			continue
		}
		var chunk dto.ChatCompletionsStreamResponse
		require.NoError(t, common.UnmarshalJsonStr(payload, &chunk))
		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil {
				finishReasons = append(finishReasons, *choice.FinishReason)
			}
		}
	}
	require.Len(t, finishReasons, 1)
	assert.Equal(t, types.FinishReasonStop, finishReasons[0])
}
