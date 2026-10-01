package middleware

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 测试环境：正文写 t.TempDir()，关闭后台清理，worker 数固定。
// ---------------------------------------------------------------------------

func requestLogTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("REQUEST_LOG_DIR", t.TempDir())
	t.Setenv("REQUEST_LOG_SWEEP_INTERVAL_SEC", "0")
	t.Setenv("REQUEST_LOG_WRITERS", "2")
	t.Setenv("REQUEST_LOG_MAX_INFLIGHT", "100")
	model.InitRequestLogStore()
	require.True(t, model.RequestLogStoreReady())
	_, _ = model.ClearAllRequestLogs()
	StartRequestLogWriters()
	t.Cleanup(func() {
		StopRequestLogWriters()
		_, _ = model.ClearAllRequestLogs()
	})
}

func enableRequestLog(t *testing.T, filterUsername string) {
	t.Helper()
	prevEnabled, prevUser, prevKB := common.RequestLogEnabled, common.RequestLogUsername, common.RequestLogMaxBodyKB
	common.RequestLogEnabled = true
	common.RequestLogUsername = filterUsername
	if common.RequestLogMaxBodyKB <= 0 {
		common.RequestLogMaxBodyKB = 64
	}
	t.Cleanup(func() {
		common.RequestLogEnabled = prevEnabled
		common.RequestLogUsername = prevUser
		common.RequestLogMaxBodyKB = prevKB
	})
}

// recordedRequestLogs 排空写队列后返回当前索引内容。
func recordedRequestLogs(t *testing.T) []*model.RequestLog {
	t.Helper()
	require.Zero(t, DrainRequestLogQueue(3*time.Second), "write queue must drain")
	logs, _, err := model.GetAllRequestLogs("", "", 0, "", 0, 0, 0, 0, 100)
	require.NoError(t, err)
	return logs
}

// ---------------------------------------------------------------------------
// isTextualContentType — 纯逻辑
// ---------------------------------------------------------------------------

func TestIsTextualContentType(t *testing.T) {
	require.True(t, isTextualContentType(""))
	require.True(t, isTextualContentType("application/json"))
	require.True(t, isTextualContentType("text/plain; charset=utf-8"))
	require.True(t, isTextualContentType("text/event-stream"))
	require.True(t, isTextualContentType("application/x-www-form-urlencoded"))
	require.True(t, isTextualContentType("application/xml"))
	require.False(t, isTextualContentType("application/octet-stream"))
	require.False(t, isTextualContentType("image/png"))
}

// ---------------------------------------------------------------------------
// appendTruncatedMark / truncateString / readLimited / headersToString
// ---------------------------------------------------------------------------

func TestAppendTruncatedMark(t *testing.T) {
	require.Equal(t, "body", appendTruncatedMark("body", false))
	require.Equal(t, "body\n...[truncated]", appendTruncatedMark("body", true))
}

func TestTruncateString(t *testing.T) {
	require.Equal(t, "abc", truncateString("abc", 5))
	require.Equal(t, "ab", truncateString("abcd", 2))
	require.Equal(t, "abcd", truncateString("abcd", 0)) // max<=0 -> unchanged
}

func TestReadBodyPrefix_ZeroLimit(t *testing.T) {
	r := strings.NewReader("abc")
	data, eof, err := readBodyPrefix(r, 0, 3)
	require.NoError(t, err)
	require.Nil(t, data)
	require.False(t, eof)
	require.Equal(t, 3, r.Len(), "a zero limit must not consume the body")
}

// readBodyPrefix 按已知大小精确分配；大小提示只影响分配，不能影响结果——
// 提示偏小或偏大时内容与 EOF 判定都必须与实际读到的一致。
func TestReadBodyPrefix_SizeHint(t *testing.T) {
	const limit = 9 // 日志上限 8 字节 + 1 字节截断探测
	cases := []struct {
		name string
		body string
		hint int64
		want string
		eof  bool
	}{
		{"empty", "", 0, "", true},
		{"one byte", "a", 1, "a", true},
		{"exactly max", "abcdefgh", 8, "abcdefgh", true},
		{"exactly limit", "abcdefghi", 9, "abcdefghi", false},
		{"far over limit", strings.Repeat("x", 100), 100, "xxxxxxxxx", false},
		{"unknown size under limit", "abc", -1, "abc", true},
		{"unknown size exactly limit", "abcdefghi", -1, "abcdefghi", false},
		{"unknown size over limit", "abcdefghij", -1, "abcdefghi", false},
		{"hint understates", "abcdef", 2, "abcdef", true},
		{"hint understates past limit", "abcdefghij", 2, "abcdefghi", false},
		{"hint overstates", "abc", 50, "abc", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := strings.NewReader(tc.body)
			data, eof, err := readBodyPrefix(r, limit, tc.hint)
			require.NoError(t, err)
			require.Equal(t, tc.want, string(data))
			require.Equal(t, tc.eof, eof)
			// 只读到上限，剩余部分留给下游。
			require.Equal(t, len(tc.body)-len(tc.want), r.Len())
			if tc.hint < 0 || tc.hint >= int64(len(tc.body)) {
				// 提示偏小时的补读是回退路径，允许扩容；其余情况一次分配封顶。
				require.LessOrEqual(t, cap(data), limit, "allocation must be bounded by the limit")
			}
		})
	}
}

// 读错误（客户端断开、外层 MaxBytesReader 超限）原样返回，已读到的字节保留。
func TestReadBodyPrefix_ReadErrorReturned(t *testing.T) {
	boom := errors.New("client went away")
	r := io.MultiReader(strings.NewReader("ab"), iotest.ErrReader(boom))
	data, eof, err := readBodyPrefix(r, 9, -1)
	require.ErrorIs(t, err, boom)
	require.False(t, eof)
	require.Equal(t, "ab", string(data))

	// 提示偏小后的补读阶段同样上报错误。
	r = io.MultiReader(strings.NewReader("abcd"), iotest.ErrReader(boom))
	data, eof, err = readBodyPrefix(r, 9, 1)
	require.ErrorIs(t, err, boom)
	require.False(t, eof)
	require.Equal(t, "abcd", string(data))
}

// 客户端没发完 Content-Length / chunked 请求体就断开时，net/http 返回
// io.ErrUnexpectedEOF。它不是正常结束：必须作为读错误上报，不能判成已读完。
func TestReadBodyPrefix_UnexpectedEOFIsAReadError(t *testing.T) {
	cases := []struct {
		name string
		body string
		hint int64
	}{
		{"unknown size", `{"model":"gpt`, -1},
		{"declared size larger than sent", `{"model":"gpt`, 200},
		{"hint understates, cut during top-up", `{"model":"gpt`, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := io.MultiReader(strings.NewReader(tc.body), iotest.ErrReader(io.ErrUnexpectedEOF))
			data, eof, err := readBodyPrefix(r, 64, tc.hint)
			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			require.False(t, eof, "a truncated body must not be reported as fully read")
			require.Equal(t, tc.body, string(data))
		})
	}

	// A reader that hands back the final bytes together with io.EOF still ends cleanly.
	data, eof, err := readBodyPrefix(iotest.DataErrReader(strings.NewReader("abc")), 64, -1)
	require.NoError(t, err)
	require.True(t, eof)
	require.Equal(t, "abc", string(data))
}

// 下游（relay）读到的是回放的前缀加上原始错误，而不是干净的 EOF；日志大小也不能
// 按"已读完"记录。
func TestCaptureRequestBody_ClientDisconnectSurfacesToRelay(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	sent := `{"model":"gpt`
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	ctx.Request.Body = io.NopCloser(io.MultiReader(strings.NewReader(sent), iotest.ErrReader(io.ErrUnexpectedEOF)))
	ctx.Request.ContentLength = 200
	ctx.Request.Header.Set("Content-Type", "application/json")

	rc := captureRequestBody(ctx, 1024)
	require.Equal(t, sent, rc.content)

	data, err := io.ReadAll(ctx.Request.Body)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF, "relay must see the read error, not a clean EOF")
	require.Equal(t, sent, string(data))
	_, drained := rc.tap.consumed()
	require.False(t, drained)
	require.EqualValues(t, 200, rc.size(), "not drained: declared length is the best estimate")
}

// 小请求体不能再按上限分配：以前每个请求固定 make 64 KB。
func TestReadBodyPrefix_SmallBodyAllocatesSmall(t *testing.T) {
	data, _, _ := readBodyPrefix(strings.NewReader(`{"a":1}`), 64*1024+1, 7)
	require.LessOrEqual(t, cap(data), 8)
}

func TestHeadersToString(t *testing.T) {
	require.Equal(t, "", headersToString(http.Header{}, 1024))
	h := http.Header{"X-Test": []string{"v"}}
	require.Contains(t, headersToString(h, 1024), "X-Test")
}

// 头部此前不受 RequestLogMaxBodyKB 限制，与"每个字段都截断到该大小"的设置文案不符，
// 也让单条日志的内存上限无法估算。
func TestHeadersToString_RespectsLimit(t *testing.T) {
	h := http.Header{"X-Big": []string{strings.Repeat("v", 5000)}}
	got := headersToString(h, 100)
	require.True(t, strings.HasSuffix(got, "...[truncated]"))
	require.Len(t, got, 100+len("\n...[truncated]"))

	// limit <= 0 表示不截断
	require.NotContains(t, headersToString(h, 0), "[truncated]")
}

// ---------------------------------------------------------------------------
// responseBodyWriter — 缓存语义
// ---------------------------------------------------------------------------

func TestResponseBodyWriter_CaptureAndTruncate(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	w := &responseBodyWriter{ResponseWriter: ctx.Writer, body: bytes.NewBuffer(nil), limit: 4}
	_, _ = w.Write([]byte("abcdef"))
	require.Equal(t, "abcd", w.body.String())
	require.True(t, w.truncated)
	require.EqualValues(t, 6, w.totalSize)
	require.Equal(t, "abcdef", rec.Body.String())
}

func TestResponseBodyWriter_WriteStringUnderLimit(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	w := &responseBodyWriter{ResponseWriter: ctx.Writer, body: bytes.NewBuffer(nil), limit: 100}
	_, _ = w.WriteString("hello")
	require.Equal(t, "hello", w.body.String())
	require.False(t, w.truncated)
}

func TestResponseBodyWriter_ZeroLimitNoCapture(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	w := &responseBodyWriter{ResponseWriter: ctx.Writer, body: bytes.NewBuffer(nil), limit: 0}
	_, _ = w.Write([]byte("data"))
	require.Equal(t, "", w.body.String())
	require.EqualValues(t, 4, w.totalSize) // 仍统计字节数
}

// ---------------------------------------------------------------------------
// 用户名过滤（纯函数，不触碰缓存/DB）
// ---------------------------------------------------------------------------

func TestRequestLogUsernameMatches(t *testing.T) {
	prev := common.RequestLogUsername
	t.Cleanup(func() { common.RequestLogUsername = prev })

	common.RequestLogUsername = "" // 空过滤器匹配全部
	require.True(t, requestLogUsernameMatches(""))
	require.True(t, requestLogUsernameMatches("anyone"))

	common.RequestLogUsername = "alice"
	require.True(t, requestLogUsernameMatches("alice"))
	require.True(t, requestLogUsernameMatches(" Alice ")) // 大小写不敏感、去空白
	require.False(t, requestLogUsernameMatches("bob"))
	require.False(t, requestLogUsernameMatches(""))

	common.RequestLogUsername = "  alice  " // 过滤器自身也去空白
	require.True(t, requestLogUsernameMatches("alice"))
}

func TestResolveRequestLogUsername_InvalidId(t *testing.T) {
	require.Equal(t, "", resolveRequestLogUsername(0))
	require.Equal(t, "", resolveRequestLogUsername(-1))
}

// ---------------------------------------------------------------------------
// captureRequestBody
// ---------------------------------------------------------------------------

func TestCaptureRequestBody_TextualResets(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"a":1}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	t.Cleanup(func() { common.CleanupBodyStorage(ctx) })

	rc := captureRequestBody(ctx, 1024)
	require.Equal(t, `{"a":1}`, rc.content)
	require.False(t, rc.truncated)
	require.EqualValues(t, 7, rc.size())

	// 请求体被拼回，后续 relay 仍可完整读取
	data, _ := io.ReadAll(ctx.Request.Body)
	require.Equal(t, `{"a":1}`, string(data))
	require.EqualValues(t, 7, rc.size())
}

func TestCaptureRequestBody_NonTextualOmitted(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := "binary"
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/octet-stream")
	t.Cleanup(func() { common.CleanupBodyStorage(ctx) })

	rc := captureRequestBody(ctx, 4)
	require.Equal(t, "[non-textual request body omitted]", rc.content)
	require.False(t, rc.truncated)
	// 下游未读：取声明的 Content-Length
	require.EqualValues(t, len(body), rc.size())
	data, _ := io.ReadAll(ctx.Request.Body)
	require.Equal(t, body, string(data), "non-textual body is passed through untouched")
	require.EqualValues(t, len(body), rc.size())
}

func TestCaptureRequestBody_NilBody(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	ctx.Request.Body = nil
	rc := captureRequestBody(ctx, 1024)
	require.Equal(t, "", rc.content)
	require.False(t, rc.truncated)
	require.Zero(t, rc.size())
	require.Nil(t, ctx.Request.Body)
}

// 压缩请求解压后，声明的 Content-Length 是压缩前的长度：下游没读完时，记录的大小
// 不能小于实际已读到的字节数；读完后以实际字节数为准。
func TestCaptureRequestBody_SizeWhenDeclaredLengthUnderstates(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := strings.Repeat("d", 5000)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	ctx.Request.ContentLength = 100
	ctx.Request.Header.Set("Content-Type", "application/json")

	rc := captureRequestBody(ctx, 1024)
	require.True(t, rc.truncated)
	require.Equal(t, body[:1024], rc.content)
	require.EqualValues(t, 1025, rc.size(), "unread: max(declared, read)")

	data, err := io.ReadAll(ctx.Request.Body)
	require.NoError(t, err)
	require.Equal(t, body, string(data))
	require.EqualValues(t, len(body), rc.size(), "drained: exact bytes read")
}

func TestCaptureRequestBody_NoBodyLeftAlone(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	ctx.Request.Body = http.NoBody
	rc := captureRequestBody(ctx, 1024)
	require.Zero(t, rc.size())
	require.Equal(t, http.NoBody, ctx.Request.Body, "http.NoBody must stay recognisable downstream")
}

// ---------------------------------------------------------------------------
// captureResponseBody
// ---------------------------------------------------------------------------

func TestCaptureResponseBody_NonTextual(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	rbw := &responseBodyWriter{ResponseWriter: ctx.Writer, body: bytes.NewBuffer(nil), limit: 1024}
	rbw.Header().Set("Content-Type", "image/png")
	_, _ = rbw.Write([]byte{0x89, 0x50})
	require.Equal(t, "[non-textual response body omitted]", captureResponseBody(ctx, rbw))
}

func TestCaptureResponseBody_Textual(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	rbw := &responseBodyWriter{ResponseWriter: ctx.Writer, body: bytes.NewBuffer(nil), limit: 1024}
	rbw.Header().Set("Content-Type", "application/json")
	_, _ = rbw.Write([]byte(`{"ok":true}`))
	require.Equal(t, `{"ok":true}`, captureResponseBody(ctx, rbw))
}

// ---------------------------------------------------------------------------
// RequestResponseLogger — 中间件全流程
// ---------------------------------------------------------------------------

func TestRequestResponseLogger_DisabledPassthrough(t *testing.T) {
	requestLogTestEnv(t)
	prev := common.RequestLogEnabled
	common.RequestLogEnabled = false
	t.Cleanup(func() { common.RequestLogEnabled = prev })

	ran := false
	r := gin.New()
	r.POST("/r", RequestResponseLogger(), func(c *gin.Context) { ran = true; c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/r", strings.NewReader(`{"x":1}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	require.True(t, ran)
	require.Empty(t, recordedRequestLogs(t), "disabled logger must record nothing")
}

func TestRequestResponseLogger_RecordsRequestAndResponse(t *testing.T) {
	requestLogTestEnv(t)
	enableRequestLog(t, "")

	r := gin.New()
	r.POST("/r", RequestResponseLogger(), func(c *gin.Context) {
		c.Set("username", "alice")
		c.Set("original_model", "gpt-4o")
		c.Set("channel_id", 7)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/r?a=1", strings.NewReader(`{"x":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-test")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	logs := recordedRequestLogs(t)
	require.Len(t, logs, 1)
	require.Equal(t, "alice", logs[0].Username)
	require.Equal(t, "gpt-4o", logs[0].ModelName)
	require.Equal(t, 7, logs[0].ChannelId)
	require.Equal(t, http.MethodPost, logs[0].Method)
	require.Equal(t, "/r?a=1", logs[0].Url)
	require.Equal(t, http.StatusOK, logs[0].StatusCode)
	require.EqualValues(t, 7, logs[0].RequestBodySize)

	detail, err := model.GetRequestLogById(logs[0].Id)
	require.NoError(t, err)
	require.Equal(t, `{"x":1}`, detail.RequestBody)
	require.Contains(t, detail.ResponseBody, "ok")
	// 明确不脱敏：请求头按原样记录（见 docs/design 第 17 节的取舍）
	require.Contains(t, detail.RequestHeaders, "Bearer sk-test")
}

// use_time_ms 此前恒为 0（从未被赋值）。计时点在中间件自身，覆盖鉴权与分发在内的
// 完整端到端耗时，不依赖 distributor 写的 context key（鉴权失败的请求走不到那里）。
func TestRequestResponseLogger_RecordsUseTime(t *testing.T) {
	requestLogTestEnv(t)
	enableRequestLog(t, "")

	r := gin.New()
	r.GET("/r", RequestResponseLogger(), func(c *gin.Context) {
		time.Sleep(25 * time.Millisecond)
		c.Status(http.StatusOK)
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/r", nil))

	logs := recordedRequestLogs(t)
	require.Len(t, logs, 1)
	require.GreaterOrEqual(t, logs[0].UseTimeMs, int64(20), "must measure the whole handler chain")
}

// 下游 panic 时也必须留下日志——这恰恰是最需要看请求体的场景。
// 历史实现把记录逻辑放在 c.Next() 之后的顺序代码里，栈展开会直接跳过。
func TestRequestResponseLogger_RecordsPanickingRequest(t *testing.T) {
	requestLogTestEnv(t)
	enableRequestLog(t, "")

	r := gin.New()
	r.Use(gin.Recovery())
	r.POST("/r", RequestResponseLogger(), func(c *gin.Context) {
		panic("boom")
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/r", strings.NewReader(`{"x":1}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)

	logs := recordedRequestLogs(t)
	require.Len(t, logs, 1, "a panicking request must still be logged")
	require.Equal(t, http.StatusInternalServerError, logs[0].StatusCode)

	detail, err := model.GetRequestLogById(logs[0].Id)
	require.NoError(t, err)
	require.Equal(t, `{"x":1}`, detail.RequestBody)
}

func TestRequestResponseLogger_UsernameFilter(t *testing.T) {
	requestLogTestEnv(t)
	enableRequestLog(t, "alice")

	r := gin.New()
	r.GET("/r", RequestResponseLogger(), func(c *gin.Context) {
		c.Set("username", c.Query("u"))
		c.Status(http.StatusOK)
	})
	for _, user := range []string{"bob", "alice", ""} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/r?u="+user, nil))
	}

	logs := recordedRequestLogs(t)
	require.Len(t, logs, 1, "only the filtered username is recorded")
	require.Equal(t, "alice", logs[0].Username)
}

func TestRequestResponseLogger_NonTextualResponse(t *testing.T) {
	requestLogTestEnv(t)
	enableRequestLog(t, "")

	r := gin.New()
	r.GET("/r", RequestResponseLogger(), func(c *gin.Context) {
		c.Data(http.StatusOK, "image/png", []byte{0x89, 0x50, 0x4e, 0x47})
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/r", nil))

	logs := recordedRequestLogs(t)
	require.Len(t, logs, 1)
	require.EqualValues(t, 4, logs[0].ResponseBodySize)

	detail, err := model.GetRequestLogById(logs[0].Id)
	require.NoError(t, err)
	require.Equal(t, "[non-textual response body omitted]", detail.ResponseBody)
}
