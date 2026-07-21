package service

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// midjourney.go — MJ action/model conversions + DoMidjourneyHttpRequest.
// Pure conversions plus one httptest-backed upstream call. No real network.
// ===========================================================================

func TestMJ_CovertMjpActionToModelName(t *testing.T) {
	assert.Equal(t, "mj_imagine", CovertMjpActionToModelName(constant.MjActionImagine))
	assert.Equal(t, "mj_upscale", CovertMjpActionToModelName(constant.MjActionUpscale))
	// SwapFace is special-cased.
	assert.Equal(t, "swap_face", CovertMjpActionToModelName(constant.MjActionSwapFace))
}

func TestMJ_ConvertSimpleChangeParams(t *testing.T) {
	// Upscale index 2.
	p := ConvertSimpleChangeParams("abc123 U2")
	require.NotNil(t, p)
	assert.Equal(t, "abc123", p.TaskId)
	assert.Equal(t, "UPSCALE", p.Action)
	assert.Equal(t, 2, p.Index)

	// Variation index 3.
	p = ConvertSimpleChangeParams("id V3")
	require.NotNil(t, p)
	assert.Equal(t, "VARIATION", p.Action)
	assert.Equal(t, 3, p.Index)

	// Reroll returns early without index.
	p = ConvertSimpleChangeParams("id R")
	require.NotNil(t, p)
	assert.Equal(t, "REROLL", p.Action)

	// Invalid: wrong token count.
	assert.Nil(t, ConvertSimpleChangeParams("only-one"))
	// Invalid: unknown action letter.
	assert.Nil(t, ConvertSimpleChangeParams("id X2"))
	// Invalid: index out of range.
	assert.Nil(t, ConvertSimpleChangeParams("id U9"))
	// Invalid: non-numeric index.
	assert.Nil(t, ConvertSimpleChangeParams("id Uz"))
}

func TestMJ_CoverPlusActionToNormalAction(t *testing.T) {
	// Upsample => upscale with parsed index.
	req := &dto.MidjourneyRequest{CustomId: "MJ::JOB::upsample::2::uuid"}
	assert.Nil(t, CoverPlusActionToNormalAction(req))
	assert.Equal(t, constant.MjActionUpscale, req.Action)
	assert.Equal(t, 2, req.Index)

	// variation with index.
	req = &dto.MidjourneyRequest{CustomId: "MJ::JOB::variation::3::uuid"}
	assert.Nil(t, CoverPlusActionToNormalAction(req))
	assert.Equal(t, constant.MjActionVariation, req.Action)
	assert.Equal(t, 3, req.Index)

	// low_variation.
	req = &dto.MidjourneyRequest{CustomId: "MJ::JOB::low_variation::1::uuid"}
	assert.Nil(t, CoverPlusActionToNormalAction(req))
	assert.Equal(t, constant.MjActionLowVariation, req.Action)

	// high_variation.
	req = &dto.MidjourneyRequest{CustomId: "MJ::JOB::high_variation::1::uuid"}
	assert.Nil(t, CoverPlusActionToNormalAction(req))
	assert.Equal(t, constant.MjActionHighVariation, req.Action)

	// pan.
	req = &dto.MidjourneyRequest{CustomId: "MJ::JOB::pan_left::1::uuid"}
	assert.Nil(t, CoverPlusActionToNormalAction(req))
	assert.Equal(t, constant.MjActionPan, req.Action)

	// reroll.
	req = &dto.MidjourneyRequest{CustomId: "MJ::JOB::reroll::1::uuid"}
	assert.Nil(t, CoverPlusActionToNormalAction(req))
	assert.Equal(t, constant.MjActionReRoll, req.Action)

	// Outpaint => zoom.
	req = &dto.MidjourneyRequest{CustomId: "MJ::Outpaint::50::1::uuid"}
	assert.Nil(t, CoverPlusActionToNormalAction(req))
	assert.Equal(t, constant.MjActionZoom, req.Action)

	// CustomZoom.
	req = &dto.MidjourneyRequest{CustomId: "MJ::CustomZoom::1::uuid"}
	assert.Nil(t, CoverPlusActionToNormalAction(req))
	assert.Equal(t, constant.MjActionCustomZoom, req.Action)

	// Inpaint.
	req = &dto.MidjourneyRequest{CustomId: "MJ::Inpaint::1::uuid"}
	assert.Nil(t, CoverPlusActionToNormalAction(req))
	assert.Equal(t, constant.MjActionInPaint, req.Action)
}

func TestMJ_CoverPlusActionToNormalAction_Errors(t *testing.T) {
	// Empty custom id.
	assert.NotNil(t, CoverPlusActionToNormalAction(&dto.MidjourneyRequest{CustomId: ""}))
	// upsample with non-numeric index.
	assert.NotNil(t, CoverPlusActionToNormalAction(&dto.MidjourneyRequest{CustomId: "MJ::JOB::upsample::x::uuid"}))
	// Unknown action.
	assert.NotNil(t, CoverPlusActionToNormalAction(&dto.MidjourneyRequest{CustomId: "MJ::JOB::totally_unknown::1::uuid"}))
}

func TestMJ_GetMjRequestModel(t *testing.T) {
	// Direct mode: imagine.
	model, errResp, ok := GetMjRequestModel(relayconstant.RelayModeMidjourneyImagine, &dto.MidjourneyRequest{})
	assert.True(t, ok)
	assert.Nil(t, errResp)
	assert.Equal(t, "mj_imagine", model)

	// Fetch modes => empty model, ok=true, no error.
	model, errResp, ok = GetMjRequestModel(relayconstant.RelayModeMidjourneyTaskFetch, &dto.MidjourneyRequest{})
	assert.True(t, ok)
	assert.Nil(t, errResp)
	assert.Equal(t, "", model)

	// Unknown relay mode => error.
	_, errResp, ok = GetMjRequestModel(-999, &dto.MidjourneyRequest{})
	assert.False(t, ok)
	assert.NotNil(t, errResp)

	// Action mode (plus request) resolves via custom id.
	req := &dto.MidjourneyRequest{CustomId: "MJ::JOB::upsample::1::uuid"}
	model, errResp, ok = GetMjRequestModel(relayconstant.RelayModeMidjourneyAction, req)
	assert.True(t, ok)
	assert.Nil(t, errResp)
	assert.Equal(t, "mj_upscale", model)

	// Action mode with bad custom id surfaces the wrapped error.
	_, errResp, ok = GetMjRequestModel(relayconstant.RelayModeMidjourneyAction, &dto.MidjourneyRequest{CustomId: ""})
	assert.False(t, ok)
	assert.NotNil(t, errResp)

	// SimpleChange with invalid content => error.
	_, errResp, ok = GetMjRequestModel(relayconstant.RelayModeMidjourneySimpleChange, &dto.MidjourneyRequest{Content: "bad"})
	assert.False(t, ok)
	assert.NotNil(t, errResp)

	// SimpleChange valid.
	model, _, ok = GetMjRequestModel(relayconstant.RelayModeMidjourneySimpleChange, &dto.MidjourneyRequest{Content: "id U1"})
	assert.True(t, ok)
	assert.Equal(t, "mj_upscale", model)
}

// --- DoMidjourneyHttpRequest (httptest) ------------------------------------

func TestMJ_DoMidjourneyHttpRequest_Success(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = readAll(r)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":1,"description":"submitted","result":"tid-1"}`))
	}))
	defer srv.Close()
	InitHttpClient()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/mj/submit/imagine",
		bytes.NewBufferString(`{"prompt":"a cat","accountFilter":{"x":1}}`))
	c.Request.Header.Set("Content-Type", "application/json")

	resp, body, err := DoMidjourneyHttpRequest(c, 5*time.Second, srv.URL)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.EqualValues(t, 1, resp.Response.Code)
	assert.Contains(t, string(body), "submitted")
	// accountFilter is stripped by default (MjAccountFilterEnabled=false).
	assert.NotContains(t, string(gotBody), "accountFilter")
}

func TestMJ_DoMidjourneyHttpRequest_EmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// empty body
	}))
	defer srv.Close()
	InitHttpClient()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/mj/submit/imagine",
		bytes.NewBufferString(`{"prompt":"x"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	resp, _, err := DoMidjourneyHttpRequest(c, 5*time.Second, srv.URL)
	require.NoError(t, err)
	require.NotNil(t, resp)
	// Empty-body path returns an MJ error wrapper with the upstream status code.
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestMJ_DoMidjourneyHttpRequest_BadRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/mj/submit/imagine",
		bytes.NewBufferString(`not-json`))
	c.Request.Header.Set("Content-Type", "application/json")

	resp, _, err := DoMidjourneyHttpRequest(c, 5*time.Second, "http://127.0.0.1:1/never")
	assert.Error(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

// readAll drains an http.Request body.
func readAll(r *http.Request) ([]byte, error) {
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}
