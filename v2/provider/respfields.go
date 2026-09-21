package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/gtkit/json/v2"
)

// OpenAI 兼容路径的响应体由 go-openai 读取并解码进它自己的结构体，平台扩展字段
// 在到达本库之前就被丢弃了（ResponseMetadata 只取响应头）。这里是与
// extrafields.go 对称的反向通道：provider 侧在发起请求前把一个收集容器写入
// context，HTTP 层在响应返回时从原始字节里取出关心的扩展字段填进去。
//
// 只有本次请求确实需要这些字段时容器才存在，其余请求在 doer 里第一步就短路，
// 不读、不解析、不额外分配。

// maxSSEScanLine 是单条 SSE 行参与扫描的字节上限。超出的行直接放弃扫描
// （仍原样透传给下游）：搜索来源不会出现在超长行里，而无上限的累积会让
// 一条畸形的长流把内存吃光。
const maxSSEScanLine = 1 << 20

type searchCaptureCtxKey struct{}

// searchCapture 收集响应体中的平台扩展搜索来源。
// 非流式路径在 doer 内一次性写入，流式路径随行扫描增量写入；读取发生在
// 响应组装处，与写入通常同一 goroutine，但容器本身仍加锁——go-openai 未来
// 若在别的 goroutine 读流，这里不该成为竞态源。
type searchCapture struct {
	// streaming 决定响应体的处理方式，由发起方按调用形态显式给出，
	// 不靠嗅探 Content-Type：嗅错会让流式响应被整体读入而挂死。
	streaming bool

	mu   sync.Mutex
	srcs []SearchSource
}

func newSearchCapture(streaming bool) *searchCapture {
	return &searchCapture{streaming: streaming}
}

// withSearchCapture 把收集容器写入 context。capture 为 nil 时原样返回。
func withSearchCapture(ctx context.Context, capture *searchCapture) context.Context {
	if capture == nil {
		return ctx
	}

	return context.WithValue(ctx, searchCaptureCtxKey{}, capture)
}

// sources 返回已收集到的来源副本；容器为 nil 或未收集到时返回 nil。
func (c *searchCapture) sources() []SearchSource {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.srcs) == 0 {
		return nil
	}

	return append([]SearchSource(nil), c.srcs...)
}

// absorb 收下一批来源。平台可能在多个 chunk 里重复回传同一批结果，
// 后到的整批覆盖先到的，而不是追加——追加会让来源列表出现重复项。
func (c *searchCapture) absorb(srcs []SearchSource) {
	if len(srcs) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.srcs = srcs
}

// compatSearchItem 是兼容平台回传的一条搜索来源。
// 字段名取自智谱文档；其余平台若落到同名字段可直接复用。
type compatSearchItem struct {
	Title string `json:"title"`
	Link  string `json:"link"`
}

// compatSearchEnvelope 覆盖来源可能出现的三个落点：响应顶层、非流式的
// choices[].message、流式的 choices[].delta。平台文档只给出了独立搜索端点的
// 顶层示例，Chat 路径与流式的落点未经实测，因此三处都查而不是认定其一。
type compatSearchEnvelope struct {
	SearchResult []compatSearchItem `json:"search_result"`
	Choices      []struct {
		Message struct {
			SearchResult []compatSearchItem `json:"search_result"`
		} `json:"message"`
		Delta struct {
			SearchResult []compatSearchItem `json:"search_result"`
		} `json:"delta"`
	} `json:"choices"`
}

// scanSearchSources 从一段 JSON 中取出搜索来源。解析失败、字段缺失或形状不符
// 一律返回 nil：来源是附加信息，畸形的扩展字段不能让本来成功的调用失败。
func scanSearchSources(body []byte) []SearchSource {
	var envelope compatSearchEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil
	}
	items := envelope.SearchResult
	for _, choice := range envelope.Choices {
		if len(items) > 0 {
			break
		}
		if len(choice.Message.SearchResult) > 0 {
			items = choice.Message.SearchResult
			continue
		}
		items = choice.Delta.SearchResult
	}
	if len(items) == 0 {
		return nil
	}

	out := make([]SearchSource, 0, len(items))
	for _, item := range items {
		out = append(out, SearchSource{URL: item.Link, Title: item.Title})
	}

	return out
}

// searchCaptureDoer 包装底层 HTTPDoer，在请求 context 携带收集容器时从响应体
// 取出搜索来源；未携带容器的请求原样透传。
type searchCaptureDoer struct {
	next HTTPDoer
}

// Do 透传请求，并在需要时从响应体旁路取出搜索来源。
// 传输层错误原样返回、不包装：这一层不产生新的失败语义，包装只会让错误链
// 多出一层重复前缀（同一条链上 extraFieldsDoer 已经包过）。
//
//nolint:wrapcheck // 透明装饰器：传输层错误保持原值语义
func (d *searchCaptureDoer) Do(req *http.Request) (*http.Response, error) {
	resp, err := d.next.Do(req)
	if err != nil {
		return nil, err
	}
	capture, ok := req.Context().Value(searchCaptureCtxKey{}).(*searchCapture)
	if !ok || capture == nil || resp.Body == nil || resp.StatusCode != http.StatusOK {
		return resp, nil
	}
	if capture.streaming {
		resp.Body = &sseSearchScanner{src: resp.Body, capture: capture}

		return resp, nil
	}

	body, readErr := io.ReadAll(resp.Body)
	if err := errors.Join(readErr, resp.Body.Close()); err != nil {
		return nil, fmt.Errorf("search capture: read response body: %w", err)
	}
	capture.absorb(scanSearchSources(body))
	resp.Body = io.NopCloser(bytes.NewReader(body))

	return resp, nil
}

// sseSearchScanner 在透传 SSE 响应体的同时按行扫描搜索来源。
// 字节流原样交给下游，扫描只在旁路进行：不缓冲整条流，也不改变任何一个字节。
type sseSearchScanner struct {
	src     io.ReadCloser
	capture *searchCapture

	// line 累积当前未终结的行；overflow 表示当前行已超长，剩余部分直接丢弃。
	line     []byte
	overflow bool
}

// Read 透传底层读取结果。错误原样返回、不包装：这是一个对下游透明的装饰器，
// go-openai 的流读取会把这里的 err 一路交回调用方，包装会让 io.EOF 之外的
// 传输层错误多出一层与故障无关的前缀，也让调用方基于错误值的判定变得脆弱。
//
//nolint:wrapcheck // 透明装饰器：错误必须保持原值语义
func (s *sseSearchScanner) Read(p []byte) (int, error) {
	n, err := s.src.Read(p)
	if n > 0 {
		s.consume(p[:n])
	}

	return n, err
}

//nolint:wrapcheck // 同 Read：关闭错误原样交还给 go-openai 的流关闭路径
func (s *sseSearchScanner) Close() error {
	return s.src.Close()
}

// consume 把新读到的字节切成行。p 的底层数组由调用方复用，
// 累积时必须拷贝（append 到 s.line 即完成拷贝）。
func (s *sseSearchScanner) consume(chunk []byte) {
	for len(chunk) > 0 {
		idx := bytes.IndexByte(chunk, '\n')
		if idx < 0 {
			s.grow(chunk)

			return
		}
		s.grow(chunk[:idx])
		s.flush()
		chunk = chunk[idx+1:]
	}
}

func (s *sseSearchScanner) grow(b []byte) {
	if s.overflow {
		return
	}
	if len(s.line)+len(b) > maxSSEScanLine {
		s.overflow = true
		s.line = s.line[:0]

		return
	}
	s.line = append(s.line, b...)
}

// sseDataPrefix 是 SSE 数据行的前缀，扫描只关心这一种行。
var sseDataPrefix = []byte("data:")

// flush 扫描一条完整的行。行缓冲在扫描完成后才复位——复位会让后续写入覆盖
// 同一段底层数组，提前复位就要求读者自证"扫描不会读到被覆盖的字节"。
func (s *sseSearchScanner) flush() {
	defer func() {
		s.line = s.line[:0]
		s.overflow = false
	}()
	if s.overflow {
		return
	}
	line := bytes.TrimSuffix(s.line, []byte("\r"))
	if !bytes.HasPrefix(line, sseDataPrefix) {
		return
	}
	payload := bytes.TrimSpace(line[len(sseDataPrefix):])
	if len(payload) == 0 || payload[0] != '{' {
		return
	}
	s.capture.absorb(scanSearchSources(payload))
}
