package api

import (
	"errors"
	"strings"
	"testing"
)

func TestSSEData(t *testing.T) {
	cases := []struct {
		line   string
		want   string
		wantOK bool
	}{
		{"data: {\"a\":1}\n", "{\"a\":1}", true},
		{"data:{\"a\":1}\n", "{\"a\":1}", true},
		{"data: [DONE]\r\n", "[DONE]", true},
		{"event: message\n", "", false},
		{": keep-alive\n", "", false},
		{"\n", "", false},
	}
	for _, c := range cases {
		got, ok := sseData([]byte(c.line))
		if ok != c.wantOK || (ok && string(got) != c.want) {
			t.Errorf("sseData(%q) = %q,%v，期望 %q,%v", c.line, got, ok, c.want, c.wantOK)
		}
	}
}

func TestParseStreamUsage(t *testing.T) {
	cases := []struct {
		name       string
		payload    string
		wantPrompt int
		wantComp   int
		wantOK     bool
	}{
		{
			name:       "正常用量",
			payload:    `{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":7}}`,
			wantPrompt: 12, wantComp: 7, wantOK: true,
		},
		{
			name:    "usage 为 null",
			payload: `{"choices":[{"delta":{"content":"x"}}],"usage":null}`,
		},
		{
			name:    "没有 usage 字段",
			payload: `{"choices":[{"delta":{"content":"x"}}]}`,
		},
		{
			name:    "非 JSON",
			payload: `[DONE]`,
		},
		{
			name:    "空载荷",
			payload: ``,
		},
		{
			name:       "缺少 completion_tokens",
			payload:    `{"usage":{"prompt_tokens":5}}`,
			wantPrompt: 5, wantComp: 0, wantOK: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prompt, comp, ok := parseStreamUsage([]byte(c.payload))
			if ok != c.wantOK || prompt != c.wantPrompt || comp != c.wantComp {
				t.Fatalf("得到 %d,%d,%v，期望 %d,%d,%v",
					prompt, comp, ok, c.wantPrompt, c.wantComp, c.wantOK)
			}
		})
	}
}

func TestStreamCapability(t *testing.T) {
	capability := newStreamCapability()
	if capability.isUnsupported("up-1") {
		t.Fatal("未标记的上游不应被拒绝")
	}
	capability.markUnsupported("up-1")
	if !capability.isUnsupported("up-1") {
		t.Fatal("标记之后应当被拒绝")
	}
	if capability.isUnsupported("up-2") {
		t.Fatal("标记只应影响被标记的上游")
	}
}

func TestForwardSSEForwardsChunksAndHoldsDone(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"你"}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"好"}}]}`,
		"",
		`data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":7}}`,
		"",
		"data: [DONE]",
		"",
		"",
	}, "\n")

	var out strings.Builder
	outcome := forwardSSE(&out, func() {}, strings.NewReader(upstream))

	if !outcome.usageSeen || outcome.promptTokens != 12 || outcome.completionTokens != 7 {
		t.Fatalf("用量解析错误：%+v", outcome)
	}
	if string(outcome.doneLine) != "data: [DONE]\n" {
		t.Fatalf("[DONE] 应被扣住，得到 %q", outcome.doneLine)
	}
	if strings.Contains(out.String(), "[DONE]") {
		t.Fatal("用量确认之前不能把 [DONE] 转发给客户端")
	}
	if !strings.Contains(out.String(), "你") || !strings.Contains(out.String(), "好") {
		t.Fatalf("内容块应原样转发，实际：%q", out.String())
	}
	// 带用量的 chunk 属于标准 OpenAI 流的一部分，客户端可能也要用它，
	// 所以照常转发；被扣住的只有 [DONE]。
	if !strings.Contains(out.String(), `"usage"`) {
		t.Fatal("带用量的 chunk 应原样转发")
	}
}

func TestForwardSSEWithoutUsageHoldsDone(t *testing.T) {
	upstream := "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\ndata: [DONE]\n\n"

	var out strings.Builder
	outcome := forwardSSE(&out, func() {}, strings.NewReader(upstream))

	if outcome.usageSeen {
		t.Fatal("上游没给用量，不应认为拿到了")
	}
	if string(outcome.doneLine) != "data: [DONE]\n" {
		t.Fatalf("[DONE] 应被扣住，得到 %q", outcome.doneLine)
	}
	if strings.Contains(out.String(), "[DONE]") {
		t.Fatal("没有用量时绝不能把 [DONE] 交给客户端")
	}
}

func TestForwardSSEPropagatesWriteError(t *testing.T) {
	upstream := "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"
	outcome := forwardSSE(failingWriter{}, func() {},
		strings.NewReader(upstream))
	if outcome.writeErr == nil {
		t.Fatal("写失败应当被记录，调用方据此判断客户端已断开")
	}
}

func TestForwardSSEKeepsEmptyLines(t *testing.T) {
	upstream := "data: {\"x\":1}\n\ndata: {\"x\":2}\n\n"
	var out strings.Builder
	forwardSSE(&out, func() {}, strings.NewReader(upstream))
	if strings.Count(out.String(), "\n\n") != 2 {
		t.Fatalf("空行是 SSE 的事件分隔符，不能被吞掉：%q", out.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }
