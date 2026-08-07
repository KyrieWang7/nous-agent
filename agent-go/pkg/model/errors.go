package model

import "errors"

// 供应商错误的分类哨兵。适配层负责把 HTTP 状态码与错误体映射到这些哨兵，
// pkg/modelrouter 据此决定恢复策略（设计文档 §11.2）：
//
//   - ErrContextOverflow    → 强制压缩后重发（上限 2 次，不计入熔断失败）
//   - ErrRateLimited        → 指数退避重试，不立即降级
//   - ErrProviderUnavailable→ 走降级链
//   - ErrSafetyTerminated   → 交由 SafetyFinishReason 中间件处理，不重试
//
// 一律用 %w 包装后返回，调用方用 errors.Is 判定，不做字符串匹配。
var (
	ErrContextOverflow     = errors.New("model: context length exceeded")
	ErrRateLimited         = errors.New("model: rate limited")
	ErrProviderUnavailable = errors.New("model: provider unavailable")
	ErrSafetyTerminated    = errors.New("model: safety terminated")
	ErrAuthFailed          = errors.New("model: authentication failed")
	ErrInvalidRequest      = errors.New("model: invalid request")
)

// IsContextOverflow 报告 err 链上是否有 ErrContextOverflow。
func IsContextOverflow(err error) bool { return errors.Is(err, ErrContextOverflow) }

// IsRateLimited 报告 err 链上是否有 ErrRateLimited。
func IsRateLimited(err error) bool { return errors.Is(err, ErrRateLimited) }

// IsProviderUnavailable 报告 err 链上是否有 ErrProviderUnavailable。
func IsProviderUnavailable(err error) bool { return errors.Is(err, ErrProviderUnavailable) }

// IsSafetyTerminated 报告 err 链上是否有 ErrSafetyTerminated。
func IsSafetyTerminated(err error) bool { return errors.Is(err, ErrSafetyTerminated) }

// IsRetryable 报告该错误是否值得原样重试（不换模型、不改请求）。
// 只有限流属于此类：其余要么需要改请求（超限），要么需要换供应商（不可用）。
func IsRetryable(err error) bool { return IsRateLimited(err) }
