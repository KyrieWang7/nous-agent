package model

// StreamEvent is an application event; handler chooses its HTTP/SSE encoding.
type StreamEvent struct {
	Name string
	ID   int64
	Data any
}
