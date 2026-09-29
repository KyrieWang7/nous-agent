package deepseek

import (
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"io"
	"strings"
	"testing"
)

func TestMalformedStreamsFail(t *testing.T) {
	start := "data: {\"type\":\"message_start\",\"message\":{}}\n\n"
	for name, body := range map[string]string{
		"eof":                   start,
		"invalid-json":          start + "data: nope\n\n",
		"error-event":           start + "data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\"}}\n\n",
		"no-stop-reason":        start + "data: {\"type\":\"message_stop\"}\n\n",
		"unterminated-terminal": start + "data: {\"type\":\"message_stop\"}",
		"unstarted-block":       "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"x\"}}\n\n",
		"mismatched-type":       start + "event: message_delta\ndata: {\"type\":\"message_stop\"}\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			s := newStream(io.NopCloser(strings.NewReader(body)), "ds")
			defer s.Close()
			if resp, err := s.Result(); err == nil || resp != nil {
				t.Fatalf("resp=%v err=%v", resp, err)
			}
			if _, ok := s.Next(); ok {
				t.Fatal("failed reader did not terminate")
			}
		})
	}
}
func TestToolDeltaIndexMatchesStart(t *testing.T) {
	raw := `data: {"type":"message_start","message":{}}

data: {"type":"content_block_start","index":4,"content_block":{"type":"thinking","thinking":"hmm"}}

data: {"type":"content_block_stop","index":4}

data: {"type":"content_block_start","index":9,"content_block":{"type":"tool_use","id":"x","name":"read","input":{}}}

data: {"type":"content_block_delta","index":9,"delta":{"type":"input_json_delta","partial_json":"{}"}}

data: {"type":"content_block_stop","index":9}

data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}

data: {"type":"message_stop"}

`
	s := newStream(io.NopCloser(strings.NewReader(raw)), "ds")
	defer s.Close()
	count := 0
	for {
		ev, ok := s.Next()
		if !ok {
			break
		}
		if ev.Type == model.StreamToolCallDelta {
			count++
			if ev.ToolCallIndex != 1 {
				t.Fatalf("event=%+v", ev)
			}
		}
	}
	if _, err := s.Result(); err != nil || count != 2 {
		t.Fatalf("err=%v count=%d", err, count)
	}
}
