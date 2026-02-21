package programawaresample

import (
	"sync"
	"time"
)

const (
	DefaultEWMAAlpha = 0.2
)

// ProgramMetrics tracks request metrics for a specific program.
type ProgramMetrics struct {
	TotalRequests   int64
	DispatchedCount int64
	AverageWaitTime time.Duration
	TotalInputTokens  int64
	TotalOutputTokens int64

	mu sync.RWMutex
}

// RegisterNewRequest increments the total request count.
func (m *ProgramMetrics) RegisterNewRequest() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TotalRequests++
}

// RegisterRequestDispatched updates the dispatched count and EWMA wait time.
func (m *ProgramMetrics) RegisterRequestDispatched(waitTime time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DispatchedCount++
	if m.DispatchedCount == 1 {
		m.AverageWaitTime = waitTime
	} else {
		m.AverageWaitTime = time.Duration(DefaultEWMAAlpha*float64(waitTime) + (1-DefaultEWMAAlpha)*float64(m.AverageWaitTime))
	}
}

// RegisterRequestComplete records token usage for a completed request.
func (m *ProgramMetrics) RegisterRequestComplete(inputTokens, outputTokens int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TotalInputTokens += int64(inputTokens)
	m.TotalOutputTokens += int64(outputTokens)
}
