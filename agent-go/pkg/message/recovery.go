package message

// RecoverInterruptedTools closes persisted tool intents that have no durable
// result. It never repeats an external operation to guess its outcome.
func RecoverInterruptedTools(history []Message) []Message {
	out := CloneAll(history)
	answered := map[string]bool{}
	for _, msg := range out {
		if msg.Role == RoleTool {
			answered[msg.ToolCallID] = true
		}
	}
	for _, msg := range history {
		for _, call := range msg.ToolCalls {
			if !answered[call.ID] {
				out = append(out, Message{Role: RoleTool, ToolCallID: call.ID, Name: call.Name, IsError: true,
					Content: "Worker interrupted before a durable result. Outcome unknown. Verify external state before retrying any operation with side effects.", AdditionalKwargs: map[string]any{"tool_recovery_code": "TOOL_OUTCOME_UNKNOWN"}})
				answered[call.ID] = true
			}
		}
	}
	return out
}
