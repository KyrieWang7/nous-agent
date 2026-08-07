package builtin

import (
	"context"
	"fmt"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
)

// NameLoopDetection 是循环检测中间件的名字。
const NameLoopDetection = "loopDetection"

// LoopDetection 检测重复的工具调用并打断。
//
// 它是启发式保护，不是安全上限：内核里的 MaxIterations 才是不可删的兜底
// （设计文档 §3.3）。删掉本中间件只是少一层"提前发现模型卡住了"的能力。
//
// 判定是**无状态**的，每次从转录推导。中间件实例在服务端被多个会话共享，
// 把计数器放在实例字段上就是一个跨会话的数据竞争。
type LoopDetection struct {
	threshold int
	window    int
}

// LoopDetectionOptions 配置循环检测。
type LoopDetectionOptions struct {
	// Threshold 是同一个调用签名重复多少次算卡住。<= 0 时用 3。
	Threshold int

	// Window 是回溯检查的 assistant 轮数。<= 0 时用 6。
	Window int
}

// NewLoopDetection 返回循环检测中间件。
func NewLoopDetection(opts LoopDetectionOptions) *LoopDetection {
	if opts.Threshold <= 0 {
		opts.Threshold = 3
	}
	if opts.Window <= 0 {
		opts.Window = 6
	}
	return &LoopDetection{threshold: opts.Threshold, window: opts.Window}
}

// Name 实现 middleware.Middleware。
func (l *LoopDetection) Name() string { return NameLoopDetection }

// Grade 实现 middleware.Graded。
//
// 监听类：检测逻辑自身出错不该判死一个正常的回合。
func (l *LoopDetection) Grade() middleware.Grade { return middleware.GradeListener }

// AfterModel 实现 middleware.AfterModel。
func (l *LoopDetection) AfterModel(_ context.Context, st *middleware.State) error {
	if st.ModelOutput == nil || len(st.ModelOutput.Message.ToolCalls) == 0 || st.History == nil {
		return nil
	}

	sig, ok := repeatedSignature(st.History.All(), l.window, l.threshold)
	if !ok {
		return nil
	}

	// 清掉本轮的工具调用并说明原因，让模型换一条路而不是继续撞墙。
	st.ModelOutput.Message.ToolCalls = nil

	note := fmt.Sprintf(
		"The same tool call (%s) has been issued %d times with identical arguments and is not making progress. "+
			"Stop repeating it: either use a different approach or explain to the user what is blocking you.",
		sig, l.threshold)

	if st.ModelOutput.Message.Content == "" {
		st.ModelOutput.Message.Content = note
	} else {
		st.ModelOutput.Message.Content += "\n\n" + note
	}
	st.History.ReplaceLastAssistant(st.ModelOutput.Message)
	return nil
}

// repeatedSignature 报告最近 window 个 assistant 轮里是否有签名重复达 threshold 次。
func repeatedSignature(msgs []message.Message, window, threshold int) (string, bool) {
	counts := make(map[string]int)
	seen := 0

	for i := len(msgs) - 1; i >= 0 && seen < window; i-- {
		m := msgs[i]
		if m.Role != message.RoleAssistant {
			continue
		}
		seen++

		for _, tc := range m.ToolCalls {
			// 签名含参数：同一个工具用不同参数调用多次是正常的探索行为，
			// 只有连参数都一模一样才说明卡住了。
			sig := tc.Name + " " + string(tc.Arguments)
			counts[sig]++
			if counts[sig] >= threshold {
				return sig, true
			}
		}
	}
	return "", false
}
