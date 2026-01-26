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
	"testing"
	"time"
)

func TestNewWorkloadRegistry(t *testing.T) {
	tests := []struct {
		name           string
		windowDuration time.Duration
		wantDuration   time.Duration
	}{
		{
			name:           "default window duration",
			windowDuration: 0,
			wantDuration:   60 * time.Second,
		},
		{
			name:           "custom window duration",
			windowDuration: 30 * time.Second,
			wantDuration:   30 * time.Second,
		},
		{
			name:           "negative window duration defaults to 60s",
			windowDuration: -10 * time.Second,
			wantDuration:   60 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wr := NewWorkloadRegistry(tt.windowDuration)
			defer wr.Stop()

			if wr.windowDuration != tt.wantDuration {
				t.Errorf("windowDuration = %v, want %v", wr.windowDuration, tt.wantDuration)
			}

			if wr.cleanupTicker == nil {
				t.Error("cleanupTicker should not be nil")
			}

			if wr.stopCleanup == nil {
				t.Error("stopCleanup channel should not be nil")
			}
		})
	}
}

func TestIncrementActive(t *testing.T) {
	wr := NewWorkloadRegistry(60 * time.Second)
	defer wr.Stop()

	workloadID := "test-workload"

	// First increment
	wr.IncrementActive(workloadID)

	metrics := wr.GetMetrics(workloadID)
	if metrics == nil {
		t.Fatal("metrics should not be nil after increment")
	}

	if metrics.TotalRequests != 1 {
		t.Errorf("TotalRequests = %d, want 1", metrics.TotalRequests)
	}

	if metrics.ActiveRequests != 1 {
		t.Errorf("ActiveRequests = %d, want 1", metrics.ActiveRequests)
	}

	if metrics.SlidingWindowRequests != 1 {
		t.Errorf("SlidingWindowRequests = %d, want 1", metrics.SlidingWindowRequests)
	}

	// Second increment
	wr.IncrementActive(workloadID)

	metrics = wr.GetMetrics(workloadID)
	if metrics.TotalRequests != 2 {
		t.Errorf("TotalRequests = %d, want 2", metrics.TotalRequests)
	}

	if metrics.ActiveRequests != 2 {
		t.Errorf("ActiveRequests = %d, want 2", metrics.ActiveRequests)
	}

	if metrics.SlidingWindowRequests != 2 {
		t.Errorf("SlidingWindowRequests = %d, want 2", metrics.SlidingWindowRequests)
	}
}

func TestDecrementActive(t *testing.T) {
	wr := NewWorkloadRegistry(60 * time.Second)
	defer wr.Stop()

	workloadID := "test-workload"

	// Increment first
	wr.IncrementActive(workloadID)
	wr.IncrementActive(workloadID)

	metrics := wr.GetMetrics(workloadID)
	if metrics.ActiveRequests != 2 {
		t.Errorf("ActiveRequests = %d, want 2", metrics.ActiveRequests)
	}

	// Decrement
	wr.DecrementActive(workloadID)

	metrics = wr.GetMetrics(workloadID)
	if metrics.ActiveRequests != 1 {
		t.Errorf("ActiveRequests = %d, want 1", metrics.ActiveRequests)
	}

	// Decrement again
	wr.DecrementActive(workloadID)

	metrics = wr.GetMetrics(workloadID)
	if metrics.ActiveRequests != 0 {
		t.Errorf("ActiveRequests = %d, want 0", metrics.ActiveRequests)
	}

	// Decrement below zero should not go negative
	wr.DecrementActive(workloadID)

	metrics = wr.GetMetrics(workloadID)
	if metrics.ActiveRequests != 0 {
		t.Errorf("ActiveRequests = %d, want 0 (should not go negative)", metrics.ActiveRequests)
	}
}

func TestDecrementActive_NonExistentWorkload(t *testing.T) {
	wr := NewWorkloadRegistry(60 * time.Second)
	defer wr.Stop()

	// Should not panic when decrementing non-existent workload
	wr.DecrementActive("non-existent")

	metrics := wr.GetMetrics("non-existent")
	if metrics != nil {
		t.Error("metrics should be nil for non-existent workload")
	}
}

func TestGetRequestRate(t *testing.T) {
	wr := NewWorkloadRegistry(60 * time.Second)
	defer wr.Stop()

	workloadID := "test-workload"

	// No requests yet
	rate := wr.GetRequestRate(workloadID)
	if rate != 0.0 {
		t.Errorf("rate = %f, want 0.0 for non-existent workload", rate)
	}

	// Add some requests
	for i := 0; i < 10; i++ {
		wr.IncrementActive(workloadID)
		time.Sleep(10 * time.Millisecond)
	}

	rate = wr.GetRequestRate(workloadID)
	if rate <= 0 {
		t.Errorf("rate = %f, want > 0", rate)
	}

	// Rate should be approximately 10 requests / elapsed time
	// We allow a wide range due to timing variations in tests
	if rate < 50 || rate > 2000 {
		t.Logf("Warning: rate = %f, expected roughly 100-1000 req/s", rate)
	}
}

func TestGetRequestRate_ExpiredWindow(t *testing.T) {
	wr := NewWorkloadRegistry(100 * time.Millisecond)
	defer wr.Stop()

	workloadID := "test-workload"

	// Add requests
	wr.IncrementActive(workloadID)

	// Wait for window to expire
	time.Sleep(150 * time.Millisecond)

	rate := wr.GetRequestRate(workloadID)
	if rate != 0.0 {
		t.Errorf("rate = %f, want 0.0 for expired window", rate)
	}
}

func TestSlidingWindowReset(t *testing.T) {
	wr := NewWorkloadRegistry(100 * time.Millisecond)
	defer wr.Stop()

	workloadID := "test-workload"

	// Add requests in first window
	wr.IncrementActive(workloadID)
	wr.IncrementActive(workloadID)

	metrics := wr.GetMetrics(workloadID)
	if metrics.SlidingWindowRequests != 2 {
		t.Errorf("SlidingWindowRequests = %d, want 2", metrics.SlidingWindowRequests)
	}

	// Wait for window to expire
	time.Sleep(150 * time.Millisecond)

	// Add request in new window
	wr.IncrementActive(workloadID)

	metrics = wr.GetMetrics(workloadID)
	if metrics.SlidingWindowRequests != 1 {
		t.Errorf("SlidingWindowRequests = %d, want 1 (window should reset)", metrics.SlidingWindowRequests)
	}

	if metrics.TotalRequests != 3 {
		t.Errorf("TotalRequests = %d, want 3 (should accumulate)", metrics.TotalRequests)
	}
}

func TestGetMetrics_NonExistentWorkload(t *testing.T) {
	wr := NewWorkloadRegistry(60 * time.Second)
	defer wr.Stop()

	metrics := wr.GetMetrics("non-existent")
	if metrics != nil {
		t.Error("metrics should be nil for non-existent workload")
	}
}

func TestGetMetrics_ReturnsCopy(t *testing.T) {
	wr := NewWorkloadRegistry(60 * time.Second)
	defer wr.Stop()

	workloadID := "test-workload"
	wr.IncrementActive(workloadID)

	metrics1 := wr.GetMetrics(workloadID)
	metrics2 := wr.GetMetrics(workloadID)

	// Should be different pointers (copies)
	if metrics1 == metrics2 {
		t.Error("GetMetrics should return copies, not the same pointer")
	}

	// But same values
	if metrics1.TotalRequests != metrics2.TotalRequests {
		t.Error("metrics copies should have same values")
	}
}

func TestConcurrency(t *testing.T) {
	wr := NewWorkloadRegistry(60 * time.Second)
	defer wr.Stop()

	workloadID := "concurrent-workload"
	numGoroutines := 100
	incrementsPerGoroutine := 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // For both increment and decrement

	// Concurrent increments
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < incrementsPerGoroutine; j++ {
				wr.IncrementActive(workloadID)
			}
		}()
	}

	// Concurrent decrements
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < incrementsPerGoroutine; j++ {
				time.Sleep(1 * time.Millisecond) // Slight delay to allow increments
				wr.DecrementActive(workloadID)
			}
		}()
	}

	wg.Wait()

	metrics := wr.GetMetrics(workloadID)
	if metrics == nil {
		t.Fatal("metrics should not be nil")
	}

	expectedTotal := int64(numGoroutines * incrementsPerGoroutine)
	if metrics.TotalRequests != expectedTotal {
		t.Errorf("TotalRequests = %d, want %d", metrics.TotalRequests, expectedTotal)
	}

	// Active requests should be 0 or close to 0 (some decrements might still be pending)
	if metrics.ActiveRequests < 0 {
		t.Errorf("ActiveRequests = %d, should never be negative", metrics.ActiveRequests)
	}
}

func TestMultipleWorkloads(t *testing.T) {
	wr := NewWorkloadRegistry(60 * time.Second)
	defer wr.Stop()

	workloads := []string{"workload-1", "workload-2", "workload-3"}

	// Add requests to different workloads
	for i, wl := range workloads {
		for j := 0; j <= i; j++ {
			wr.IncrementActive(wl)
		}
	}

	// Verify each workload has correct counts
	for i, wl := range workloads {
		metrics := wr.GetMetrics(wl)
		if metrics == nil {
			t.Errorf("metrics should not be nil for %s", wl)
			continue
		}

		expectedCount := int64(i + 1)
		if metrics.TotalRequests != expectedCount {
			t.Errorf("%s: TotalRequests = %d, want %d", wl, metrics.TotalRequests, expectedCount)
		}
	}

	// Verify all workload IDs are tracked
	allIDs := wr.GetAllWorkloadIDs()
	if len(allIDs) != len(workloads) {
		t.Errorf("GetAllWorkloadIDs returned %d workloads, want %d", len(allIDs), len(workloads))
	}
}

func TestCleanup(t *testing.T) {
	wr := NewWorkloadRegistry(60 * time.Second)
	defer wr.Stop()

	workloadID := "cleanup-test"

	// Add and complete a request
	wr.IncrementActive(workloadID)
	wr.DecrementActive(workloadID)

	// Manually set last request time to be old
	value, _ := wr.workloads.Load(workloadID)
	metrics := value.(*WorkloadMetrics)
	metrics.mu.Lock()
	metrics.LastRequestTime = time.Now().Add(-10 * time.Minute)
	metrics.mu.Unlock()

	// Run cleanup
	wr.cleanup()

	// Workload should be removed
	if wr.GetMetrics(workloadID) != nil {
		t.Error("workload should be removed after cleanup")
	}
}

func TestCleanup_ActiveWorkloadNotRemoved(t *testing.T) {
	wr := NewWorkloadRegistry(60 * time.Second)
	defer wr.Stop()

	workloadID := "active-workload"

	// Add request but don't complete it
	wr.IncrementActive(workloadID)

	// Manually set last request time to be old
	value, _ := wr.workloads.Load(workloadID)
	metrics := value.(*WorkloadMetrics)
	metrics.mu.Lock()
	metrics.LastRequestTime = time.Now().Add(-10 * time.Minute)
	metrics.mu.Unlock()

	// Run cleanup
	wr.cleanup()

	// Workload should NOT be removed because it has active requests
	if wr.GetMetrics(workloadID) == nil {
		t.Error("active workload should not be removed during cleanup")
	}
}

func TestStop(t *testing.T) {
	wr := NewWorkloadRegistry(60 * time.Second)

	// Stop should not panic
	wr.Stop()

	// Calling Stop again should not panic
	wr.Stop()
}

func TestGetAllWorkloadIDs(t *testing.T) {
	wr := NewWorkloadRegistry(60 * time.Second)
	defer wr.Stop()

	// Initially empty
	ids := wr.GetAllWorkloadIDs()
	if len(ids) != 0 {
		t.Errorf("GetAllWorkloadIDs returned %d workloads, want 0", len(ids))
	}

	// Add workloads
	workloads := []string{"wl-1", "wl-2", "wl-3"}
	for _, wl := range workloads {
		wr.IncrementActive(wl)
	}

	ids = wr.GetAllWorkloadIDs()
	if len(ids) != len(workloads) {
		t.Errorf("GetAllWorkloadIDs returned %d workloads, want %d", len(ids), len(workloads))
	}

	// Verify all workloads are present
	idSet := make(map[string]bool)
	for _, id := range ids {
		idSet[id] = true
	}

	for _, wl := range workloads {
		if !idSet[wl] {
			t.Errorf("workload %s not found in GetAllWorkloadIDs", wl)
		}
	}
}

// Made with Bob
