package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gtkit/json/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const bochaOKFixture = `{
  "code": 200,
  "log_id": "log-1",
  "msg": null,
  "data": {
    "_type": "SearchResponse",
    "queryContext": {"originalQuery": "go 1.27"},
    "webPages": {
      "webSearchUrl": "https://bochaai.com/search?q=go",
      "totalEstimatedMatches": 120,
      "value": [
        {"id":"1","name":"Go 1.27 发布","url":"https://go.dev/blog/go1.27","siteName":"go.dev",
         "snippet":"简短摘要","summary":"这是更长的正文摘要","datePublished":"2026-08-12T00:00:00Z"},
        {"id":"2","name":"只有短摘要","url":"https://example.com/a","snippet":"仅 snippet","summary":"","datePublished":""},
        {"id":"3","name":"无链接应被丢弃","url":"","snippet":"x"}
      ]
    }
  }
}`

func newBochaTestClient(t *testing.T, handler http.HandlerFunc) *BochaClient {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c, err := NewBocha(BochaConfig{APIKey: "test-key", BaseURL: srv.URL})
	require.NoError(t, err)
	return c
}

func TestNewBochaValidatesConfig(t *testing.T) {
	t.Parallel()

	_, err := NewBocha(BochaConfig{})
	require.ErrorIs(t, err, ErrMissingAPIKey)

	c, err := NewBocha(BochaConfig{APIKey: "k"})
	require.NoError(t, err)
	assert.Equal(t, bochaDefaultBaseURL, c.baseURL)
	assert.Equal(t, string(FreshnessNoLimit), c.freshness, "留空默认 noLimit")
	assert.True(t, c.summary, "默认请求长摘要，否则只能拿到几十字 snippet")

	c, err = NewBocha(BochaConfig{APIKey: "k", BaseURL: "https://gw.corp/", DisableSummary: true})
	require.NoError(t, err)
	assert.Equal(t, "https://gw.corp", c.baseURL, "末尾斜杠被裁掉")
	assert.False(t, c.summary)
}

// TestBochaSearchMapsRequestAndResponse 锁定请求形态与响应归一：
// 鉴权头、条数裁剪、长短摘要回退、无链接结果剔除、时间解析。
func TestBochaSearchMapsRequestAndResponse(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	c := newBochaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/web-search", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bochaOKFixture))
	})

	hits, err := c.Search(t.Context(), "go 1.27", 3)
	require.NoError(t, err)

	assert.Equal(t, "go 1.27", captured["query"])
	assert.Equal(t, "noLimit", captured["freshness"])
	assert.Equal(t, true, captured["summary"])
	assert.InDelta(t, 3, captured["count"], 0.0001)

	require.Len(t, hits, 2, "无 URL 的结果应被剔除")
	assert.Equal(t, "Go 1.27 发布", hits[0].Title)
	assert.Equal(t, "https://go.dev/blog/go1.27", hits[0].URL)
	assert.Equal(t, "go.dev", hits[0].Site)
	assert.Equal(t, "这是更长的正文摘要", hits[0].Text(), "有长摘要时优先用长摘要")
	assert.Equal(t, 2026, hits[0].Published.Year())

	assert.Equal(t, "仅 snippet", hits[1].Text(), "无长摘要时回退 snippet")
	assert.True(t, hits[1].Published.IsZero(), "时间缺失返回零值而非报错")
}

func TestBochaSearchClampsCount(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		limit int
		want  float64
	}{
		"零取默认":   {limit: 0, want: bochaDefaultCount},
		"负数取默认":  {limit: -5, want: bochaDefaultCount},
		"超上限被裁剪": {limit: 999, want: bochaMaxCount},
		"正常值保留":  {limit: 12, want: 12},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var captured map[string]any
			c := newBochaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
				_, _ = w.Write([]byte(bochaOKFixture))
			})
			_, err := c.Search(t.Context(), "q", tc.limit)
			require.NoError(t, err)
			assert.InDelta(t, tc.want, captured["count"], 0.0001)
		})
	}
}

func TestBochaSearchRejectsEmptyQuery(t *testing.T) {
	t.Parallel()

	c := newBochaTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("不应发出请求")
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := c.Search(t.Context(), "   ", 5)
	require.ErrorIs(t, err, ErrEmptyQuery)
}

// TestBochaSearchErrorClassification 锁定可重试判定：限流与 5xx 值得重试，
// 鉴权失败与业务码错误重试多少次都一样。
func TestBochaSearchErrorClassification(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status        int
		body          string
		wantRetryable bool
		wantContains  string
	}{
		"鉴权失败不重试":  {status: http.StatusUnauthorized, body: `{"msg":"invalid key"}`, wantContains: "http 401"},
		"参数错误不重试":  {status: http.StatusBadRequest, body: `{"msg":"bad"}`, wantContains: "http 400"},
		"限流可重试":    {status: http.StatusTooManyRequests, body: `{}`, wantRetryable: true, wantContains: "http 429"},
		"服务端错误可重试": {status: http.StatusBadGateway, body: `{}`, wantRetryable: true, wantContains: "http 502"},
		// HTTP 200 但业务码非 200：属于平台侧拒绝，不重试。
		"业务码失败不重试": {status: http.StatusOK, body: `{"code":403,"msg":"quota exhausted","log_id":"lg-9"}`, wantContains: "quota exhausted"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := newBochaTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := c.Search(t.Context(), "q", 5)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantContains)
			assert.Equal(t, tc.wantRetryable, IsRetryable(err))

			var apiErr *BochaError
			require.ErrorAs(t, err, &apiErr)
		})
	}
}

func TestBochaSearchTransportErrorIsRetryable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // 立即关闭，制造连接失败

	c, err := NewBocha(BochaConfig{APIKey: "k", BaseURL: srv.URL})
	require.NoError(t, err)

	_, err = c.Search(t.Context(), "q", 5)
	require.Error(t, err)
	assert.True(t, IsRetryable(err), "网络抖动值得重试")
}

func TestBochaSearchRespectsContextCancel(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	c := newBochaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		_, _ = w.Write([]byte(bochaOKFixture))
	})
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, err := c.Search(ctx, "q", 5)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded) || IsRetryable(err),
		"超时应经 ctx 生效，而非等待固定的客户端超时")
}

func TestBochaSearchHandlesEmptyAndMalformedPayload(t *testing.T) {
	t.Parallel()

	t.Run("空结果返回空切片", func(t *testing.T) {
		t.Parallel()
		c := newBochaTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"code":200,"data":{"webPages":{"value":[]}}}`))
		})
		hits, err := c.Search(t.Context(), "q", 5)
		require.NoError(t, err)
		assert.Empty(t, hits)
	})

	t.Run("缺 data 字段不 panic", func(t *testing.T) {
		t.Parallel()
		c := newBochaTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"code":200}`))
		})
		hits, err := c.Search(t.Context(), "q", 5)
		require.NoError(t, err)
		assert.Empty(t, hits)
	})

	t.Run("非 JSON 响应返回解码错误", func(t *testing.T) {
		t.Parallel()
		c := newBochaTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`<html>502 bad gateway</html>`))
		})
		_, err := c.Search(t.Context(), "q", 5)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode bocha response")
	})
}

// TestTruncateForError 锁定错误信息不会把整页响应灌进日志。
func TestTruncateForError(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "short", truncateForError("  short  "))
	long := truncateForError(fmt.Sprintf("%0512d", 0))
	assert.Len(t, []rune(long), 257, "256 字符 + 省略号")
}

func TestParseBochaTime(t *testing.T) {
	t.Parallel()

	assert.False(t, parseBochaTime("2026-08-12T00:00:00Z").IsZero())
	assert.False(t, parseBochaTime("2026-08-12 10:30:00").IsZero())
	assert.False(t, parseBochaTime("2026-08-12").IsZero())
	assert.True(t, parseBochaTime("").IsZero())
	assert.True(t, parseBochaTime("昨天").IsZero(), "无法解析时返回零值而不是报错")
}
