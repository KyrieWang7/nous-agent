package loop

import (
	"context"
	"errors"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
)

// DirectSampler 直接调用单个模型，不做分层、降级或重试。
//
// 它存在的意义是让内核在没有 pkg/modelrouter 的情况下可用（测试、单模型部署）。
// 生产装配用 modelrouter.Router，它实现同一个 Sampler 接口。
type DirectSampler struct {
	m model.Model
}

// NewDirectSampler 返回直连 m 的采样器。
func NewDirectSampler(m model.Model) *DirectSampler {
	return &DirectSampler{m: m}
}

// Sample 实现 Sampler。第二个返回值恒为 false：非流式路径没有内容流出去过。
func (s *DirectSampler) Sample(ctx context.Context, st *middleware.State) (*model.Response, bool, error) {
	if s.m == nil {
		return nil, false, errors.New("loop: direct sampler has no model")
	}
	if st.ModelInput == nil {
		return nil, false, errors.New("loop: model input was not built")
	}

	resp, err := s.m.Complete(ctx, *st.ModelInput)
	if err != nil {
		return nil, false, err
	}
	if resp == nil {
		return nil, false, errors.New("loop: model returned no response")
	}
	return resp, false, nil
}
