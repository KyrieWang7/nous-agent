package message

import "unicode/utf8"

// perMessageOverhead 是每条消息的固定开销（role 标记、分隔符等）。
// 与 OpenAI 的 chat 格式计费口径对齐的粗略值。
const perMessageOverhead = 4

// TokenCounter 估算文本的 token 数。
//
// 默认实现是字符数近似，够用于压缩阈值判定（判错一次的代价是多压或少压一轮，
// 不是正确性问题）。要精确计费时换成 tiktoken 实现即可，接口不变。
type TokenCounter interface {
	Count(text string) int
}

// HeuristicCounter 按字符类别近似估算：
// CJK 约 1.5 字符/token，其余约 4 字符/token。
type HeuristicCounter struct{}

// Count 实现 TokenCounter。
func (HeuristicCounter) Count(text string) int {
	return EstimateTokens(text)
}

// EstimateTokens 估算单段文本的 token 数。
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}

	var cjk, other int
	for _, r := range text {
		if isCJK(r) {
			cjk++
		} else {
			other++
		}
	}

	// cjk/1.5 + other/4，用整数运算避免浮点：(cjk*2)/3 + other/4
	n := (cjk*2)/3 + other/4
	if n == 0 && utf8.RuneCountInString(text) > 0 {
		n = 1
	}
	return n
}

// EstimateMessageTokens 估算一条消息的 token 数，含工具调用参数与多模态块。
func EstimateMessageTokens(m Message) int {
	n := perMessageOverhead
	n += EstimateTokens(m.Content)
	n += EstimateTokens(m.ReasoningContent)

	for _, b := range m.ContentBlocks {
		n += EstimateTokens(b.Text)
		// 图片按固定成本近似，不按 base64 长度 —— 后者会高估几十倍。
		if b.Type == "image" {
			n += 768
		}
	}

	for _, tc := range m.ToolCalls {
		n += EstimateTokens(tc.Name)
		n += EstimateTokens(string(tc.Arguments))
	}

	return n
}

// EstimateMessagesTokens 估算一批消息的 token 总数。
func EstimateMessagesTokens(msgs []Message) int {
	var n int
	for _, m := range msgs {
		n += EstimateMessageTokens(m)
	}
	return n
}

func isCJK(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF: // CJK 统一汉字
		return true
	case r >= 0x3400 && r <= 0x4DBF: // 扩展 A
		return true
	case r >= 0x3040 && r <= 0x30FF: // 平假名 / 片假名
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // 谚文
		return true
	case r >= 0xFF00 && r <= 0xFFEF: // 全角标点
		return true
	case r >= 0x3000 && r <= 0x303F: // CJK 标点
		return true
	default:
		return false
	}
}
