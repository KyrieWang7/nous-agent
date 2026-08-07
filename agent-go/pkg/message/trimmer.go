package message

// Trimmer 按 token 上限裁剪转录，供内核在每次采样前调用（设计文档 §3.1 步 4）。
//
// Trim 是纯函数，不失败。它与压缩（pkg/compaction）是两件不同的事：
// 压缩会重写转录并持久化摘要，Trim 只影响本次请求的投递内容，不改 History。
type Trimmer struct {
	limit   int
	counter TokenCounter
}

// NewTrimmer 返回按 limit 裁剪的 Trimmer。limit <= 0 表示不裁剪。
// counter 为 nil 时用 HeuristicCounter。
func NewTrimmer(limit int, counter TokenCounter) *Trimmer {
	if counter == nil {
		counter = HeuristicCounter{}
	}
	return &Trimmer{limit: limit, counter: counter}
}

// Trim 从头部丢弃消息直到总量落在上限内，返回可投递的消息切片。
//
// 三条规则：
//   - 首条 system 消息永久保留（它承载系统提示与缓存边界）。
//   - 切点不落在工具事务中间：半个事务会让请求非法（tool_calls 无对应结果）。
//   - 至少保留最后一条消息：即便它单条就超限，空请求更糟。
func (t *Trimmer) Trim(msgs []Message) []Message {
	if t == nil || t.limit <= 0 || len(msgs) == 0 {
		return msgs
	}

	// 首条 system 消息单独固定，不参与裁剪。
	var head []Message
	body := msgs
	if msgs[0].Role == RoleSystem {
		head = msgs[:1]
		body = msgs[1:]
	}

	budget := t.limit - t.countAll(head)
	if budget <= 0 {
		// 系统提示本身就吃满了预算：仍保留它和最后一条消息。
		return t.headPlusLast(head, body)
	}

	cut := t.findCut(body, budget)
	if cut >= len(body) {
		return t.headPlusLast(head, body)
	}

	out := make([]Message, 0, len(head)+len(body)-cut)
	out = append(out, head...)
	out = append(out, body[cut:]...)
	return out
}

// findCut 返回 body 上的起始下标，使 body[cut:] 的估算 token 数不超过 budget，
// 且 cut 不落在工具事务中间。
func (t *Trimmer) findCut(body []Message, budget int) int {
	// 从尾部往前累加，找到第一个超预算的位置。
	total := 0
	cut := len(body)
	for i := len(body) - 1; i >= 0; i-- {
		n := t.count(body[i])
		if total+n > budget {
			break
		}
		total += n
		cut = i
	}

	if cut == 0 || cut >= len(body) {
		return cut
	}

	// 切点若落在某个工具事务内部，前移到该事务起点之后（整段丢弃），
	// 避免留下没有 assistant 发起方的孤立 tool 消息。
	for _, span := range ToolTransactionSpans(body) {
		if span.Start < cut && cut < span.End {
			return span.End
		}
	}
	return cut
}

func (t *Trimmer) headPlusLast(head, body []Message) []Message {
	if len(body) == 0 {
		return head
	}
	last := body[len(body)-1]

	// 最后一条是 tool 结果时，必须连带它的 assistant 发起方，否则请求非法。
	start := len(body) - 1
	if last.Role == RoleTool {
		for _, span := range ToolTransactionSpans(body) {
			if span.Start <= start && start < span.End {
				start = span.Start
				break
			}
		}
	}

	out := make([]Message, 0, len(head)+len(body)-start)
	out = append(out, head...)
	out = append(out, body[start:]...)
	return out
}

func (t *Trimmer) count(m Message) int {
	n := perMessageOverhead
	n += t.countText(m.Content)
	n += t.countText(m.ReasoningContent)
	for _, b := range m.ContentBlocks {
		n += t.countText(b.Text)
		if b.Type == "image" {
			n += 768
		}
	}
	for _, tc := range m.ToolCalls {
		n += t.countText(tc.Name)
		n += t.countText(string(tc.Arguments))
	}
	return n
}

// countText 对空串短路，不进 counter。空串恒为 0 token，
// 而调用外部 tokenizer 是有成本的。
func (t *Trimmer) countText(s string) int {
	if s == "" {
		return 0
	}
	return t.counter.Count(s)
}

func (t *Trimmer) countAll(msgs []Message) int {
	var n int
	for _, m := range msgs {
		n += t.count(m)
	}
	return n
}
