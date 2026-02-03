/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package datastore

import (
	"sync"
	"time"
)

// WorkloadContext represents the workload identity and priority information
// extracted from the X-Workload-Context header.
type WorkloadContext struct {
	WorkloadID  string `json:"workload_id"`
	Criticality int    `json:"criticality"` // 1-5, where 5 is highest priority
}

// WorkloadMetrics tracks request metrics for a specific workload.
type WorkloadMetrics struct {
	WorkloadID            string
	TotalRequests         int64
	ActiveRequests        int64 // Requests currently in queue or being processed
	SlidingWindowRequests int64 // Requests in the current sliding window
	WindowStartTime       time.Time
	LastRequestTime       time.Time

	// Average wait time tracking (EMA)
	AverageWaitTime time.Duration // Exponential Moving Average of wait times
	DispatchedCount int64         // Total requests dispatched
	EMAAlpha        float64       // Decay factor for EMA (default: 0.2)

	mu sync.RWMutex
}

// WorkloadRegistry maintains metrics for all active workloads.
// It provides thread-safe operations for tracking request counts and rates.
type WorkloadRegistry struct {
	workloads      sync.Map // key: workload_id (string), value: *WorkloadMetrics
	windowDuration time.Duration
	cleanupTicker  *time.Ticker
	stopCleanup    chan struct{}
}

// NewWorkloadRegistry creates a new WorkloadRegistry with the specified sliding window duration.
// It starts a background goroutine to periodically clean up inactive workloads.
func NewWorkloadRegistry(windowDuration time.Duration) *WorkloadRegistry {
	if windowDuration <= 0 {
		windowDuration = 60 * time.Second // Default to 60 seconds
	}

	wr := &WorkloadRegistry{
		windowDuration: windowDuration,
		stopCleanup:    make(chan struct{}),
	}

	// Start cleanup goroutine
	wr.cleanupTicker = time.NewTicker(5 * time.Minute)
	go wr.cleanupLoop()

	return wr
}

// WorkloadHandleNewRequest increments the active request count for the given workload.
// It also updates the sliding window metrics and last request time.
func (wr *WorkloadRegistry) WorkloadHandleNewRequest(workloadID string) {
	now := time.Now()

	// Load or create workload metrics
	value, _ := wr.workloads.LoadOrStore(workloadID, &WorkloadMetrics{
		WorkloadID:      workloadID,
		WindowStartTime: now,
		LastRequestTime: now,
	})

	metrics := value.(*WorkloadMetrics)
	metrics.mu.Lock()
	defer metrics.mu.Unlock()

	// Update counters
	metrics.TotalRequests++
	metrics.ActiveRequests++
	metrics.LastRequestTime = now

	// Update sliding window
	if now.Sub(metrics.WindowStartTime) > wr.windowDuration {
		// Reset window
		metrics.WindowStartTime = now
		metrics.SlidingWindowRequests = 1
	} else {
		metrics.SlidingWindowRequests++
	}
}

// WorkloadHandleDispatchedRequest updates the average wait time when a request is dispatched.
// Uses Exponential Moving Average (EMA) for smooth, adaptive tracking.
//
// Formula: AvgWaitTime = α × CurrentWait + (1-α) × PreviousAvg
// Where α (alpha) controls sensitivity to recent changes (default: 0.2)
//
// This method should be called when a request is successfully dispatched to track
// the workload's historical wait time behavior.
func (wr *WorkloadRegistry) WorkloadHandleDispatchedRequest(workloadID string, waitTime time.Duration) {
	value, ok := wr.workloads.Load(workloadID)
	if !ok {
		return // Workload not found, nothing to update
	}

	metrics := value.(*WorkloadMetrics)
	metrics.mu.Lock()
	defer metrics.mu.Unlock()

	// Initialize alpha if not set (default: 0.2 = 20% current, 80% history)
	if metrics.EMAAlpha == 0 {
		metrics.EMAAlpha = 0.2
	}

	// First dispatch: initialize average with the first wait time
	if metrics.DispatchedCount == 0 {
		metrics.AverageWaitTime = waitTime
	} else {
		// EMA calculation: blend current wait time with historical average
		alpha := metrics.EMAAlpha
		oldAvg := float64(metrics.AverageWaitTime)
		newWait := float64(waitTime)
		metrics.AverageWaitTime = time.Duration(alpha*newWait + (1-alpha)*oldAvg)
	}

	metrics.DispatchedCount++
}

// WorkloadHandleCompletedRequest decrements the active request count for the given workload.
// It ensures the count never goes below zero.
func (wr *WorkloadRegistry) WorkloadHandleCompletedRequest(workloadID string) {
	value, ok := wr.workloads.Load(workloadID)
	if !ok {
		return
	}

	metrics := value.(*WorkloadMetrics)
	metrics.mu.Lock()
	defer metrics.mu.Unlock()

	if metrics.ActiveRequests > 0 {
		metrics.ActiveRequests--
	}
}

// GetRequestRate returns the current request rate (requests per second) for the given workload
// based on the sliding window. Returns 0.0 if the workload is not found or has no recent requests.
func (wr *WorkloadRegistry) GetRequestRate(workloadID string) float64 {
	value, ok := wr.workloads.Load(workloadID)
	if !ok {
		return 0.0
	}

	metrics := value.(*WorkloadMetrics)
	metrics.mu.RLock()
	defer metrics.mu.RUnlock()

	now := time.Now()
	windowAge := now.Sub(metrics.WindowStartTime)

	// If window is expired, return 0
	if windowAge > wr.windowDuration {
		return 0.0
	}

	// Calculate rate: requests / seconds
	if windowAge.Seconds() == 0 {
		return 0.0
	}

	return float64(metrics.SlidingWindowRequests) / windowAge.Seconds()
}

// GetMetrics returns a snapshot of the metrics for the given workload.
// Returns nil if the workload is not found.
func (wr *WorkloadRegistry) GetMetrics(workloadID string) *WorkloadMetrics {
	value, ok := wr.workloads.Load(workloadID)
	if !ok {
		return nil
	}

	metrics := value.(*WorkloadMetrics)
	metrics.mu.RLock()
	defer metrics.mu.RUnlock()

	// Return a copy to avoid race conditions
	return &WorkloadMetrics{
		WorkloadID:            metrics.WorkloadID,
		TotalRequests:         metrics.TotalRequests,
		ActiveRequests:        metrics.ActiveRequests,
		SlidingWindowRequests: metrics.SlidingWindowRequests,
		WindowStartTime:       metrics.WindowStartTime,
		LastRequestTime:       metrics.LastRequestTime,
		AverageWaitTime:       metrics.AverageWaitTime,
		DispatchedCount:       metrics.DispatchedCount,
		EMAAlpha:              metrics.EMAAlpha,
	}
}

// cleanupLoop runs periodically to remove inactive workloads from the registry.
// A workload is considered inactive if it has no active requests and hasn't
// received a request in the last 5 minutes.
func (wr *WorkloadRegistry) cleanupLoop() {
	for {
		select {
		case <-wr.cleanupTicker.C:
			wr.cleanup()
		case <-wr.stopCleanup:
			wr.cleanupTicker.Stop()
			return
		}
	}
}

// cleanup removes inactive workloads from the registry.
func (wr *WorkloadRegistry) cleanup() {
	now := time.Now()
	inactiveThreshold := 5 * time.Minute

	wr.workloads.Range(func(key, value interface{}) bool {
		metrics := value.(*WorkloadMetrics)
		metrics.mu.RLock()
		isInactive := metrics.ActiveRequests == 0 && now.Sub(metrics.LastRequestTime) > inactiveThreshold
		metrics.mu.RUnlock()

		if isInactive {
			wr.workloads.Delete(key)
		}
		return true
	})
}

// Stop stops the cleanup goroutine. Should be called when the registry is no longer needed.
// It's safe to call Stop multiple times.
func (wr *WorkloadRegistry) Stop() {
	select {
	case <-wr.stopCleanup:
		// Already stopped
		return
	default:
		close(wr.stopCleanup)
	}
}

// GetAllWorkloadIDs returns a list of all currently tracked workload IDs.
func (wr *WorkloadRegistry) GetAllWorkloadIDs() []string {
	var workloadIDs []string
	wr.workloads.Range(func(key, value interface{}) bool {
		workloadIDs = append(workloadIDs, key.(string))
		return true
	})
	return workloadIDs
}

// Made with Bob
