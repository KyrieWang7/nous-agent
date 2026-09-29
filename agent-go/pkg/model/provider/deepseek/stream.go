package deepseek

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"io"
	"strings"
)

type streamBlock struct {
	block
	closed    bool
	arguments string
	index     int
}
type stream struct {
	body                         io.ReadCloser
	scanner                      *bufio.Scanner
	modelID                      string
	blocks                       map[int]*streamBlock
	order                        []int
	started, done                bool
	err                          error
	result                       model.Response
	input, cacheRead, cacheWrite int
}

func newStream(body io.ReadCloser, modelID string) *stream {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	return &stream{body: body, scanner: scanner, modelID: modelID, blocks: map[int]*streamBlock{}}
}
func (s *stream) fail(err error) (model.StreamEvent, bool) {
	s.done = true
	s.err = err
	return model.StreamEvent{Type: model.StreamError, Err: err}, true
}
func malformed(detail string) error {
	return fmt.Errorf("%w: malformed DeepSeek Messages stream: %s", model.ErrProviderUnavailable, detail)
}

// frame consumes complete SSE frames, including multi-line data. A trailing
// unterminated frame is not a protocol terminal event.
func (s *stream) frame() (string, string, error) {
	var data []string
	event := ""
	size := 0
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if line == "" {
			if len(data) > 0 {
				return event, strings.Join(data, "\n"), nil
			}
			event = ""
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			size += len(value)
			if size > 4<<20 {
				return "", "", malformed("event too large")
			}
			data = append(data, value)
		}
	}
	if err := s.scanner.Err(); err != nil {
		return "", "", transportError(err)
	}
	return "", "", fmt.Errorf("%w: %w", model.ErrProviderUnavailable, io.ErrUnexpectedEOF)
}
func (s *stream) Next() (model.StreamEvent, bool) {
	if s.done {
		return model.StreamEvent{}, false
	}
	for {
		kind, raw, err := s.frame()
		if err != nil {
			return s.fail(err)
		}
		var ev struct {
			Type    string `json:"type"`
			Index   *int   `json:"index"`
			Content block  `json:"content_block"`
			Message struct {
				ID, Model string
				Usage     map[string]int `json:"usage"`
			} `json:"message"`
			Delta struct {
				Type, Text, Thinking, Signature string
				PartialJSON                     string `json:"partial_json"`
				StopReason                      string `json:"stop_reason"`
			} `json:"delta"`
			Usage map[string]int `json:"usage"`
		}
		if json.Unmarshal([]byte(raw), &ev) != nil || ev.Type == "" || (kind != "" && kind != ev.Type) {
			return s.fail(malformed("invalid JSON or mismatched event type"))
		}
		if ev.Type == "error" {
			return s.fail(providerError(0, []byte(raw)))
		}
		if ev.Type == "message_start" {
			if s.started {
				return s.fail(malformed("duplicate message_start"))
			}
			s.started = true
			s.result.CallID = ev.Message.ID
			s.result.ModelName = ev.Message.Model
			if err := s.usage(ev.Message.Usage); err != nil {
				return s.fail(err)
			}
			return model.StreamEvent{Type: model.StreamStart}, true
		}
		switch ev.Type {
		case "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop":
			if !s.started {
				return s.fail(malformed("event precedes message_start"))
			}
		default:
			continue
		}
		switch ev.Type {
		case "content_block_start":
			if ev.Index == nil || *ev.Index < 0 || s.blocks[*ev.Index] != nil || s.result.StopReason != "" {
				return s.fail(malformed("invalid block start"))
			}
			b := &streamBlock{block: ev.Content, index: len(s.order)}
			s.blocks[*ev.Index] = b
			s.order = append(s.order, *ev.Index)
			switch b.Type {
			case "text":
				if b.Text != "" {
					return model.StreamEvent{Type: model.StreamTextDelta, Delta: b.Text}, true
				}
			case "thinking":
				if b.Thinking != "" {
					return model.StreamEvent{Type: model.StreamThinkingDelta, Delta: b.Thinking}, true
				}
			case "tool_use":
				if b.ID == "" || b.Name == "" {
					return s.fail(malformed("empty tool identity"))
				}
				return model.StreamEvent{Type: model.StreamToolCallDelta, ToolCallIndex: len(s.order) - 1, ToolCallID: b.ID, ToolCallName: b.Name}, true
			default:
				return s.fail(malformed("unsupported block " + b.Type))
			}
		case "content_block_delta", "content_block_stop":
			if ev.Index == nil {
				return s.fail(malformed("missing block index"))
			}
			b := s.blocks[*ev.Index]
			if b == nil || b.closed {
				return s.fail(malformed("delta/stop without open block"))
			}
			if ev.Type == "content_block_stop" {
				b.closed = true
				if b.arguments != "" {
					b.Input = json.RawMessage(b.arguments)
				}
				continue
			}
			switch {
			case ev.Delta.Type == "text_delta" && b.Type == "text":
				b.Text += ev.Delta.Text
				return model.StreamEvent{Type: model.StreamTextDelta, Delta: ev.Delta.Text}, true
			case ev.Delta.Type == "thinking_delta" && b.Type == "thinking":
				b.Thinking += ev.Delta.Thinking
				return model.StreamEvent{Type: model.StreamThinkingDelta, Delta: ev.Delta.Thinking}, true
			case ev.Delta.Type == "signature_delta" && b.Type == "thinking":
				b.Signature += ev.Delta.Signature
			case ev.Delta.Type == "input_json_delta" && b.Type == "tool_use":
				b.arguments += ev.Delta.PartialJSON
				return model.StreamEvent{Type: model.StreamToolCallDelta, ToolCallIndex: b.index, ToolCallID: b.ID, ToolCallName: b.Name, ArgumentsDelta: ev.Delta.PartialJSON}, true
			default:
				return s.fail(malformed("unsupported delta " + ev.Delta.Type))
			}
		case "message_delta":
			switch ev.Delta.StopReason {
			case "end_turn", "stop_sequence":
				s.result.StopReason = model.StopReasonStop
			case "tool_use":
				s.result.StopReason = model.StopReasonToolCalls
			case "max_tokens":
				s.result.StopReason = model.StopReasonLength
			case "":
			default:
				return s.fail(malformed("unknown stop reason"))
			}
			if err := s.usage(ev.Usage); err != nil {
				return s.fail(err)
			}
		case "message_stop":
			if s.result.StopReason == "" {
				return s.fail(malformed("missing stop reason"))
			}
			msg := message.Message{Role: message.RoleAssistant}
			var replayBlocks []block
			ids := map[string]bool{}
			for _, i := range s.order {
				b := s.blocks[i]
				if !b.closed {
					return s.fail(malformed("unclosed content block"))
				}
				switch b.Type {
				case "text":
					msg.Content += b.Text
				case "thinking":
					msg.ReasoningContent += b.Thinking
				case "tool_use":
					var obj map[string]json.RawMessage
					if json.Unmarshal(b.Input, &obj) != nil || obj == nil {
						if s.result.StopReason == model.StopReasonLength {
							continue
						}
						return s.fail(malformed("invalid tool JSON"))
					}
					if ids[b.ID] {
						return s.fail(malformed("duplicate tool id"))
					}
					ids[b.ID] = true
					msg.ToolCalls = append(msg.ToolCalls, message.ToolCall{ID: b.ID, Name: b.Name, Arguments: b.Input})
				}
				replayBlocks = append(replayBlocks, b.block)
			}
			if len(replayBlocks) == 0 && s.result.StopReason == model.StopReasonStop {
				return s.fail(malformed("empty response"))
			}
			rp := replay{Model: s.modelID, Digest: digest(msg), Blocks: replayBlocks}
			// Store JSON-compatible metadata so database round-trips retain it exactly.
			raw, _ := json.Marshal(rp)
			var metadata any
			_ = json.Unmarshal(raw, &metadata)
			msg.AdditionalKwargs = map[string]any{"deepseek_replay": metadata}
			s.result.Message = msg
			if s.result.ModelName == "" {
				s.result.ModelName = s.modelID
			}
			s.done = true
			return model.StreamEvent{Type: model.StreamDone}, true
		}
	}
}
func (s *stream) usage(u map[string]int) error {
	for k, v := range u {
		if v < 0 {
			return malformed("negative token usage")
		}
		switch k {
		case "input_tokens":
			s.input = v
		case "output_tokens":
			s.result.Usage.OutputTokens = v
		case "cache_read_input_tokens":
			s.cacheRead = v
		case "cache_creation_input_tokens":
			s.cacheWrite = v
		}
	}
	s.result.Usage.InputTokens = s.input + s.cacheRead + s.cacheWrite
	s.result.Usage.CachedInputTokens = s.cacheRead
	return nil
}
func (s *stream) Result() (*model.Response, error) {
	for !s.done {
		s.Next()
	}
	if s.err != nil {
		return nil, s.err
	}
	return &s.result, nil
}
func (s *stream) Close() error { return s.body.Close() }
