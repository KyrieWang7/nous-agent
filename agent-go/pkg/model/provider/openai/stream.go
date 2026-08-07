package openai

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

// maxSSELine 是单行 SSE 数据的上限。
//
// bufio.Scanner 默认 64 KiB，而带 base64 图片或长工具参数的行会超过它 ——
// 超限时 Scanner 静默停止，表现为"回答莫名截断"。
const maxSSELine = 4 << 20

type streamChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Delta        struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// sseReader 把 OpenAI 的 SSE 流转成 model.StreamEvent 序列。
type sseReader struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
	name    string

	pending []model.StreamEvent
	started bool
	done    bool
	err     error
	closed  bool

	// 累积状态：工具调用按 index 拼装，因为增量里 name 只出现在第一片，
	// arguments 分多片到达。
	text       strings.Builder
	reasoning  strings.Builder
	calls      map[int]*partialCall
	callOrder  []int
	stopReason string
	usage      model.Usage
	callID     string
	modelName  string
}

type partialCall struct {
	id   string
	name string
	args strings.Builder
}

func newSSEReader(body io.ReadCloser, name string) *sseReader {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), maxSSELine)

	return &sseReader{
		body:    body,
		scanner: sc,
		name:    name,
		calls:   make(map[int]*partialCall),
	}
}

// Next 实现 model.StreamReader。
func (r *sseReader) Next() (model.StreamEvent, bool) {
	if r.closed {
		return model.StreamEvent{}, false
	}

	for {
		if len(r.pending) > 0 {
			ev := r.pending[0]
			r.pending = r.pending[1:]
			return ev, true
		}
		if r.done {
			return model.StreamEvent{}, false
		}
		if !r.readMore() {
			return model.StreamEvent{}, false
		}
	}
}

// readMore 读下一行并把它转成待发事件。返回 false 表示流结束。
func (r *sseReader) readMore() bool {
	if !r.scanner.Scan() {
		if err := r.scanner.Err(); err != nil {
			r.err = fmt.Errorf("openai: reading stream: %w", err)
			r.pending = append(r.pending, model.StreamEvent{Type: model.StreamError, Err: r.err})
		}
		r.done = true
		return len(r.pending) > 0
	}

	line := strings.TrimSpace(r.scanner.Text())
	if line == "" || strings.HasPrefix(line, ":") {
		return true // 心跳与注释帧
	}
	data, ok := strings.CutPrefix(line, "data:")
	if !ok {
		return true // event: / id: 之类的字段，本协议不用
	}

	payload := strings.TrimSpace(data)
	if payload == "[DONE]" {
		r.pending = append(r.pending, model.StreamEvent{Type: model.StreamDone})
		r.done = true
		return true
	}

	var chunk streamChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		// 单个坏 chunk 不该杀掉整个流：跳过并继续读。
		return true
	}

	if !r.started {
		r.started = true
		r.pending = append(r.pending, model.StreamEvent{Type: model.StreamStart})
	}
	if chunk.ID != "" && r.callID == "" {
		r.callID = chunk.ID
	}
	if chunk.Model != "" {
		r.modelName = chunk.Model
	}
	if chunk.Usage != nil {
		r.usage = model.Usage{
			InputTokens:       chunk.Usage.PromptTokens,
			OutputTokens:      chunk.Usage.CompletionTokens,
			CachedInputTokens: chunk.Usage.PromptTokensDetails.CachedTokens,
		}
	}

	for _, choice := range chunk.Choices {
		if choice.FinishReason != "" {
			r.stopReason = mapFinishReason(choice.FinishReason)
		}

		if d := choice.Delta.ReasoningContent; d != "" {
			r.reasoning.WriteString(d)
			r.pending = append(r.pending, model.StreamEvent{Type: model.StreamThinkingDelta, Delta: d})
		}
		if d := choice.Delta.Content; d != "" {
			r.text.WriteString(d)
			r.pending = append(r.pending, model.StreamEvent{Type: model.StreamTextDelta, Delta: d})
		}

		for _, tc := range choice.Delta.ToolCalls {
			pc, ok := r.calls[tc.Index]
			if !ok {
				pc = &partialCall{}
				r.calls[tc.Index] = pc
				r.callOrder = append(r.callOrder, tc.Index)
			}
			// id 与 name 通常只在第一片出现，后续片只有 arguments。
			if tc.ID != "" {
				pc.id = tc.ID
			}
			if tc.Function.Name != "" {
				pc.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				pc.args.WriteString(tc.Function.Arguments)
			}

			r.pending = append(r.pending, model.StreamEvent{
				Type:           model.StreamToolCallDelta,
				ToolCallIndex:  tc.Index,
				ToolCallID:     pc.id,
				ToolCallName:   pc.name,
				ArgumentsDelta: tc.Function.Arguments,
			})
		}
	}

	return true
}

// Result 实现 model.StreamReader。
func (r *sseReader) Result() (*model.Response, error) {
	// 未读完时先把剩余的流消费掉，否则聚合结果会缺尾部内容。
	for !r.done {
		if !r.readMore() {
			break
		}
	}
	r.pending = nil

	if r.err != nil {
		return nil, r.err
	}

	msg := message.Message{
		Role:             message.RoleAssistant,
		Content:          r.text.String(),
		ReasoningContent: r.reasoning.String(),
	}
	for _, idx := range r.callOrder {
		pc := r.calls[idx]
		args := pc.args.String()
		if args == "" {
			args = "{}"
		}
		msg.ToolCalls = append(msg.ToolCalls, message.ToolCall{
			ID:        pc.id,
			Name:      pc.name,
			Arguments: json.RawMessage(args),
		})
	}

	stop := r.stopReason
	if stop == "" {
		if len(msg.ToolCalls) > 0 {
			stop = model.StopReasonToolCalls
		} else {
			stop = model.StopReasonStop
		}
	}

	callID := r.callID
	if callID == "" {
		callID = r.name + "-stream"
	}
	modelName := r.modelName
	if modelName == "" {
		modelName = r.name
	}

	return &model.Response{
		Message:    msg,
		StopReason: stop,
		Usage:      r.usage,
		CallID:     callID,
		ModelName:  modelName,
	}, nil
}

// Close 实现 model.StreamReader。可重复调用。
func (r *sseReader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return r.body.Close()
}
