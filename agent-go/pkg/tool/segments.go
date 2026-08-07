package tool

// IndexedCall 是带原始位置的工具调用。
//
// Index 是该调用在模型输出的 tool_calls 数组里的下标。并发执行会打乱完成顺序，
// 回写结果时必须按 Index 复原 —— 模型看到的结果顺序必须与它发起的顺序一致。
type IndexedCall struct {
	Index int
	Call  Call
}

// Segment 是一批可以一起执行的调用。
//
// Concurrent 为真时段内调用并行执行；为假时段内只有一个调用，独占执行。
// 段与段之间严格顺序，前一段全部完成才进入下一段。
type Segment struct {
	Concurrent bool
	Calls      []IndexedCall
}

// Partition 把一批工具调用切分成执行段。
//
// 规则（设计文档 §7.2）：连续的"只读且并发安全"调用聚成一个并发段；
// 其余每个调用独占一段。判据来自工具元数据（Definition.Concurrent），
// 不来自模型意图 —— 模型声称"这几个可以并行"是不可信的。
//
// 未注册的工具按保守处理：独占执行。它随后会在执行阶段变成一条 error 结果，
// 但在分段阶段不能假设它是安全的。
func Partition(r *Registry, calls []Call) []Segment {
	if len(calls) == 0 {
		return nil
	}

	var segments []Segment
	var pending []IndexedCall

	flush := func() {
		if len(pending) == 0 {
			return
		}
		segments = append(segments, Segment{Concurrent: true, Calls: pending})
		pending = nil
	}

	for i, c := range calls {
		item := IndexedCall{Index: i, Call: c}

		if concurrentSafe(r, c.Name) {
			pending = append(pending, item)
			continue
		}
		flush()
		segments = append(segments, Segment{Calls: []IndexedCall{item}})
	}
	flush()

	return segments
}

func concurrentSafe(r *Registry, name string) bool {
	if r == nil {
		return false
	}
	d, err := r.Get(name)
	if err != nil {
		return false
	}
	return d.Concurrent()
}
