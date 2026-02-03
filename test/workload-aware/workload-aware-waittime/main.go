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

// Package main tests the wait time penalty component of the workload-aware policy.
//
// Test Strategy:
// Phase 1: Build wait time history for low-priority workload
// Phase 2: Saturate system with low-priority workload requests
// Phase 3: Send high-priority workload requests (with no wait history)
// Phase 4: Verify low-priority workload completes first due to accumulated wait time
//
// Scoring Formula: Score = (AvgWaitTime × 0.4) + (Criticality × 0.4) - (RequestRate × 0.2)
//
// Expected: Low-priority workload with high AvgWaitTime should score higher than
// high-priority workload with zero AvgWaitTime, demonstrating fairness mechanism.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// WorkloadConfig defines a workload with its characteristics
type WorkloadConfig struct {
	WorkloadID  string
	Criticality int
	NumRequests int
	Delay       time.Duration
}

// RequestResult tracks the result of a single request
type RequestResult struct {
	RequestID    int
	WorkloadID   string
	Criticality  int
	SendTime     time.Time
	CompleteTime time.Time
	Duration     time.Duration
	StatusCode   int
	Success      bool
	Error        error
}

// Stats tracks overall test statistics
type Stats struct {
	TotalSent    atomic.Int64
	TotalSuccess atomic.Int64
	TotalFailed  atomic.Int64
	Results      []RequestResult
	ResultsMutex sync.Mutex
}

// InferenceRequest represents the request payload
type InferenceRequest struct {
	Model       string  `json:"model"`
	Prompt      string  `json:"prompt"`
	MaxTokens   int     `json:"max_tokens"`
	Temperature float64 `json:"temperature"`
}

func main() {
	gatewayURL := flag.String("url", "http://localhost:8081/v1/completions", "Gateway URL")
	warmupDuration := flag.Duration("warmup", 20*time.Second, "Warmup duration to build wait time history")
	flag.Parse()

	fmt.Printf("=== Wait Time Penalty Test ===\n\n")
	fmt.Printf("Gateway URL: %s\n", *gatewayURL)
	fmt.Printf("Warmup Duration: %v\n\n", *warmupDuration)

	ctx := context.Background()

	// Phase 1: Warmup - Build wait time history for low-priority workload
	fmt.Printf("Phase 1: Building wait time history for low-priority workload...\n")
	runWarmupPhase(ctx, *gatewayURL, *warmupDuration)
	fmt.Printf("Phase 1 complete. Low-priority workload now has accumulated wait time.\n\n")

	// Small delay to let metrics settle
	time.Sleep(2 * time.Second)

	// Phase 2 & 3: Main test - Saturate with low-priority, then send high-priority
	fmt.Printf("Phase 2: Starting main test...\n")
	stats := &Stats{
		Results: make([]RequestResult, 0),
	}

	var wg sync.WaitGroup
	startTime := time.Now()

	// Workload A: Low criticality but has wait time history
	workloadA := WorkloadConfig{
		WorkloadID:  "low-priority-with-history",
		Criticality: 2,
		NumRequests: 40,
		Delay:       0,
	}

	// Workload B: High criticality but no wait time history
	workloadB := WorkloadConfig{
		WorkloadID:  "high-priority-no-history",
		Criticality: 4,
		NumRequests: 20,
		Delay:       5 * time.Second, // Start after A to ensure A builds queue first
	}

	fmt.Printf("Workload A: %s (criticality=%d, requests=%d, delay=%v)\n",
		workloadA.WorkloadID, workloadA.Criticality, workloadA.NumRequests, workloadA.Delay)
	fmt.Printf("Workload B: %s (criticality=%d, requests=%d, delay=%v)\n\n",
		workloadB.WorkloadID, workloadB.Criticality, workloadB.NumRequests, workloadB.Delay)

	// Launch workloads
	for _, workload := range []WorkloadConfig{workloadA, workloadB} {
		wg.Add(1)
		go func(wl WorkloadConfig) {
			defer wg.Done()
			if wl.Delay > 0 {
				time.Sleep(wl.Delay)
			}
			runWorkload(ctx, *gatewayURL, wl, stats)
		}(workload)
	}

	wg.Wait()
	totalDuration := time.Since(startTime)

	// Analyze results
	analyzeResults(stats, totalDuration)
}

// runWarmupPhase sends requests continuously to build wait time history
func runWarmupPhase(ctx context.Context, gatewayURL string, duration time.Duration) {
	endTime := time.Now().Add(duration)
	requestCount := 0

	for time.Now().Before(endTime) {
		requestCount++
		sendRequest(ctx, gatewayURL, "low-priority-with-history", 2, requestCount)
		time.Sleep(500 * time.Millisecond) // Steady rate
	}

	fmt.Printf("Warmup phase sent %d requests\n", requestCount)
}

// runWorkload sends requests for a single workload
func runWorkload(ctx context.Context, gatewayURL string, workload WorkloadConfig, stats *Stats) {
	fmt.Printf("[%s] Starting workload (criticality=%d, requests=%d)\n",
		workload.WorkloadID, workload.Criticality, workload.NumRequests)

	var wg sync.WaitGroup

	for i := 0; i < workload.NumRequests; i++ {
		wg.Add(1)
		go func(reqNum int) {
			defer wg.Done()

			result := sendRequest(ctx, gatewayURL, workload.WorkloadID, workload.Criticality, reqNum)

			stats.ResultsMutex.Lock()
			stats.Results = append(stats.Results, result)
			stats.ResultsMutex.Unlock()

			if result.Success {
				stats.TotalSuccess.Add(1)
			} else {
				stats.TotalFailed.Add(1)
			}
			stats.TotalSent.Add(1)
		}(i)
	}

	wg.Wait()
	fmt.Printf("[%s] Completed all requests\n", workload.WorkloadID)
}

// sendRequest sends a single inference request
func sendRequest(ctx context.Context, gatewayURL, workloadID string, criticality, reqNum int) RequestResult {
	result := RequestResult{
		RequestID:   reqNum,
		WorkloadID:  workloadID,
		Criticality: criticality,
		SendTime:    time.Now(),
	}

	reqBody := InferenceRequest{
		Model:       "meta-llama/Llama-3.1-8B-Instruct",
		Prompt:      fmt.Sprintf("Test request %d from %s", reqNum, workloadID),
		MaxTokens:   50,
		Temperature: 0.7,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		result.Error = err
		result.Success = false
		return result
	}

	req, err := http.NewRequestWithContext(ctx, "POST", gatewayURL, bytes.NewBuffer(jsonData))
	if err != nil {
		result.Error = err
		result.Success = false
		return result
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workload-ID", workloadID)
	req.Header.Set("X-Workload-Criticality", fmt.Sprintf("%d", criticality))

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		result.Error = err
		result.Success = false
		result.CompleteTime = time.Now()
		result.Duration = result.CompleteTime.Sub(result.SendTime)
		return result
	}
	defer resp.Body.Close()

	result.CompleteTime = time.Now()
	result.Duration = result.CompleteTime.Sub(result.SendTime)
	result.StatusCode = resp.StatusCode
	result.Success = resp.StatusCode == http.StatusOK

	io.Copy(io.Discard, resp.Body)

	return result
}

// analyzeResults analyzes and prints test results
func analyzeResults(stats *Stats, totalDuration time.Duration) {
	fmt.Printf("\n=== Test Results ===\n\n")
	fmt.Printf("Total Duration: %v\n", totalDuration)
	fmt.Printf("Total Requests: %d\n", stats.TotalSent.Load())
	fmt.Printf("  Success: %d\n", stats.TotalSuccess.Load())
	fmt.Printf("  Failed: %d\n", stats.TotalFailed.Load())

	if len(stats.Results) == 0 {
		fmt.Printf("\nNo results to analyze\n")
		return
	}

	// Sort by completion time to get completion order
	sortedResults := make([]RequestResult, len(stats.Results))
	copy(sortedResults, stats.Results)
	sort.Slice(sortedResults, func(i, j int) bool {
		return sortedResults[i].CompleteTime.Before(sortedResults[j].CompleteTime)
	})

	// Calculate average completion position by workload
	workloadPositions := make(map[string][]int)
	for pos, result := range sortedResults {
		if result.Success {
			workloadPositions[result.WorkloadID] = append(workloadPositions[result.WorkloadID], pos+1)
		}
	}

	fmt.Printf("\n=== Completion Order Analysis ===\n\n")
	fmt.Printf("Average Completion Position by Workload:\n")

	type WorkloadStats struct {
		WorkloadID  string
		Criticality int
		AvgPosition float64
		Count       int
	}

	var workloadStats []WorkloadStats
	for workloadID, positions := range workloadPositions {
		sum := 0
		for _, pos := range positions {
			sum += pos
		}
		avgPos := float64(sum) / float64(len(positions))

		// Get criticality from first result with this workload
		criticality := 0
		for _, r := range stats.Results {
			if r.WorkloadID == workloadID {
				criticality = r.Criticality
				break
			}
		}

		workloadStats = append(workloadStats, WorkloadStats{
			WorkloadID:  workloadID,
			Criticality: criticality,
			AvgPosition: avgPos,
			Count:       len(positions),
		})
	}

	// Sort by average position
	sort.Slice(workloadStats, func(i, j int) bool {
		return workloadStats[i].AvgPosition < workloadStats[j].AvgPosition
	})

	for _, ws := range workloadStats {
		fmt.Printf("  %s (criticality=%d): Avg position %.1f (count=%d)\n",
			ws.WorkloadID, ws.Criticality, ws.AvgPosition, ws.Count)
	}

	// Verify wait time penalty is working
	fmt.Printf("\n=== Wait Time Penalty Verification ===\n\n")

	var lowPriorityAvg, highPriorityAvg float64
	for _, ws := range workloadStats {
		if ws.WorkloadID == "low-priority-with-history" {
			lowPriorityAvg = ws.AvgPosition
		} else if ws.WorkloadID == "high-priority-no-history" {
			highPriorityAvg = ws.AvgPosition
		}
	}

	fmt.Printf("Expected: Low-priority workload (with wait history) should complete before high-priority workload (no history)\n")
	fmt.Printf("Actual: Low-priority avg=%.1f, High-priority avg=%.1f\n\n", lowPriorityAvg, highPriorityAvg)

	if lowPriorityAvg < highPriorityAvg {
		fmt.Printf("✅ PASS: Wait time penalty is working!\n")
		fmt.Printf("   Low-priority workload with accumulated wait time completed before high-priority workload.\n")
		fmt.Printf("   This demonstrates the fairness mechanism - workloads that wait longer get priority boost.\n")
	} else {
		fmt.Printf("❌ FAIL: Wait time penalty may not be working as expected.\n")
		fmt.Printf("   High-priority workload completed before low-priority workload despite no wait history.\n")
	}

	// Show first 20 completions
	fmt.Printf("\n=== First 20 Completions ===\n\n")
	for i := 0; i < 20 && i < len(sortedResults); i++ {
		r := sortedResults[i]
		if r.Success {
			fmt.Printf("%2d. %s (criticality=%d) - %v\n",
				i+1, r.WorkloadID, r.Criticality, r.Duration)
		}
	}
}

// Made with Bob
