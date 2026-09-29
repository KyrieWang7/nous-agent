package modelrouter

import (
	"context"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"reflect"
)

type requestCompactor interface {
	ShouldCompactRequest(model.Request, model.Info) (bool, error)
	MaybeCompactRequest(context.Context, *message.History, model.Request, model.Info) (bool, error)
}

func (r *Router) ManagesModelAttempts() bool { return true }
func (r *Router) ManagesCompaction() bool    { _, ok := r.cfg.Compactor.(requestCompactor); return ok }

// Reconcile the new trimmed canonical projection with transient insertions.
// Match retained occurrences from the end: compaction replaces a prefix, and
// equal-valued older messages must not be resurrected as retained tail entries.
func (r *Router) rebuildInput(st *lifecycle.State, before []message.Message) {
	if st.ModelInput == nil || st.History == nil {
		return
	}
	baseline := message.CloneAll(st.ModelHistory)
	if baseline == nil && st.RebuildModelHistory == nil {
		baseline = before
	}
	after := st.History.All()
	if st.RebuildModelHistory != nil {
		after = st.RebuildModelHistory()
	}
	retained := make(map[int]int)
	cursor := len(after) - 1
	for i := len(baseline) - 1; i >= 0; i-- {
		for j := cursor; j >= 0; j-- {
			if reflect.DeepEqual(baseline[i], after[j]) {
				retained[i] = j
				cursor = j - 1
				break
			}
		}
	}
	// Classify each request occurrence against the pre-handler projection.
	canonical := make(map[int]int)
	cursor = 0
	for i, m := range st.ModelInput.Messages {
		for j := cursor; j < len(baseline); j++ {
			if reflect.DeepEqual(m, baseline[j]) {
				canonical[i] = j
				cursor = j + 1
				break
			}
		}
	}
	inserts := make(map[int][]message.Message)
	seenCanonical := false
	for i, m := range st.ModelInput.Messages {
		if _, ok := canonical[i]; ok {
			seenCanonical = true
			continue
		}
		anchor := len(after)
		if !seenCanonical {
			anchor = 0
		} else {
			for j := i + 1; j < len(st.ModelInput.Messages); j++ {
				if old, ok := canonical[j]; ok {
					if pos, kept := retained[old]; kept {
						anchor = pos
						break
					}
				}
			}
		}
		inserts[anchor] = append(inserts[anchor], m)
	}
	var out []message.Message
	for i, m := range after {
		out = append(out, inserts[i]...)
		out = append(out, m)
	}
	out = append(out, inserts[len(after)]...)
	req := *st.ModelInput
	req.Messages = message.CloneAll(out)
	st.ModelInput = &req
	st.ModelHistory = message.CloneAll(after)
}
