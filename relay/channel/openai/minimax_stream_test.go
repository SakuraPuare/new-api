package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newMiniMaxStreamTest(t *testing.T, body string) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo, *http.Response) {
	t.Helper()
	oldStreamingTimeout := constant.StreamingTimeout
	if oldStreamingTimeout <= 0 {
		constant.StreamingTimeout = 30
	}
	t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeMiniMax},
		IsStream:    true,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		RelayFormat: types.RelayFormatOpenAI,
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	return c, recorder, info, resp
}

func TestOaiStreamHandlerMiniMaxBusinessErrorReturnsKeyError(t *testing.T) {
	c, _, info, resp := newMiniMaxStreamTest(t, `data: {"id":"error","choices":null,"base_resp":{"status_code":2056,"status_msg":"Token Plan usage limit reached"}}
`)

	usage, err := OaiStreamHandler(c, info, resp)
	require.Nil(t, usage)
	require.Error(t, err)
	require.Equal(t, types.ErrorCodeChannelInvalidKey, err.GetErrorCode())
	require.Equal(t, http.StatusUnauthorized, err.StatusCode)
	require.Contains(t, err.Error(), "Token Plan usage limit reached")
	require.True(t, info.StreamStatus.ResponseFailed())
}

func TestOaiStreamHandlerMiniMaxEmptyStreamReturnsBadResponse(t *testing.T) {
	c, _, info, resp := newMiniMaxStreamTest(t, "")

	usage, err := OaiStreamHandler(c, info, resp)
	require.Nil(t, usage)
	require.Error(t, err)
	require.Equal(t, types.ErrorCodeBadResponse, err.GetErrorCode())
	require.Equal(t, http.StatusBadGateway, err.StatusCode)
	require.Contains(t, err.Error(), "empty stream")
}

func TestOaiStreamHandlerMiniMaxNonKeyBusinessErrorIsNotAutoBanned(t *testing.T) {
	c, _, info, resp := newMiniMaxStreamTest(t, `data: {"base_resp":{"status_code":2013,"status_msg":"invalid params"}}
`)

	usage, err := OaiStreamHandler(c, info, resp)
	require.Nil(t, usage)
	require.Error(t, err)
	require.Equal(t, types.ErrorCodeBadResponse, err.GetErrorCode())
	require.Equal(t, http.StatusBadGateway, err.StatusCode)
}

func TestOaiStreamHandlerMiniMaxNormalStreamSucceeds(t *testing.T) {
	body := "data: {\"id\":\"ok\",\"choices\":[{\"delta\":{\"content\":\"pong\"}}]}\n" +
		"data: {\"id\":\"ok\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n" +
		"data: [DONE]\n"
	c, recorder, info, resp := newMiniMaxStreamTest(t, body)

	usage, err := OaiStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Contains(t, recorder.Body.String(), "pong")
	require.Equal(t, "completed", info.StreamStatus.ResponseOutcome())
}
