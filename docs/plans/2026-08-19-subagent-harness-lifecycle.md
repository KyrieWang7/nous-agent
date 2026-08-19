# Subagent Harness Lifecycle Closure

## Understanding

- Nous Agent uses Go to apply the runtime principles of DeepSeek Agent Harness while retaining an explicit Agent Loop.
- A production subagent currently constructs a child `harness.Harness` but returns only its `loop.Runner`.
- The discarded Harness owns a runtime generation and plugin manager, so its `Close` lifecycle cannot run.
- Child resources must remain live through execution and be released exactly once after success, failure, cancellation, or panic.
- Synchronous and asynchronous dispatch share the same `Manager.dispatch` path and must have identical ownership behavior.
- Existing Runner factories remain supported for embedders that do not own resources.

## Assumptions

- Release is constant-time relative to model execution and does not change dispatch concurrency.
- A release failure after child execution is a coordination failure; it does not rewrite a completed child execution as failed.
- A release failure before a terminal child result is available is joined with the primary dispatch error.
- This change does not alter HTTP, SSE, model-loop, tool, or persistence protocols.

## Decision

Add a prepared runner value containing a `Runner` and an optional release callback. Add a prepared-runner factory constructor without changing the existing factory signatures. The Subagent Manager owns the prepared value, installs an exactly-once release guard immediately after construction, and releases it after child execution but before terminal coordination and persistence.

The production factory returns the child Harness runner together with `Harness.Close`. Kernel ownership remains unchanged: `loop.Runner` does not gain a `Close` method.

## Alternatives

1. Add `Close` to `loop.Runner`. Rejected because it moves runtime resource ownership into the Kernel.
2. Cache one Harness per subagent profile. Rejected because run-scoped configuration and generation ownership become coupled across concurrent dispatches.
3. Keep returning a bare Runner. Rejected because the runtime generation and plugin lifecycle remain unreachable.

## Error Handling

- Factory failure: no release callback exists.
- Failure after preparation but before execution: deferred fallback releases the prepared runner and joins any release error.
- Execution success or failure: release runs explicitly before terminal coordination and persistence.
- Panic: the existing child panic recovery converts the panic to an execution error, then normal release runs.
- Release failure after execution: preserve the execution status, add `CoordinationError`, and return the joined error.

## Verification

- Release once after successful execution.
- Release once after model/loop failure.
- Release once after recovered panic.
- Release errors are reported without changing a successful child execution status.
- Legacy Runner factories continue to pass their existing tests.
- Production assembly uses the prepared-runner factory.

## Decision Log

- Runtime resources are owned by the Subagent Manager for the lifetime of one dispatch.
- Kernel APIs remain resource-agnostic.
- Backward compatibility is limited to existing Go factory constructors; no protocol compatibility layer is added.
- Whole-Harness caching is outside this change.
