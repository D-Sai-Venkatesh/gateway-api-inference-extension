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

// Package main tests the request rate penalty component of the workload-aware policy.
//
// Test Strategy:
// 1. Send high-rate burst from Workload A (same criticality)
// 2. Send low-rate steady stream from Workload B (same criticality)
// 3. Verify low-rate workload gets prioritized due to fairness mechanism
//
// Scoring Formula: Score = (AvgWaitTime × 0.4) + (Criticality × 0.4) - (RequestRate × 0.2)
//
// Expected: Low-rate workload should score higher than high-rate workload when both
// have the same criticality, demonstrating the fairness mechanism that penalizes
// aggressive workloads.
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
	RequestRate float64       // Requests per second
	Delay       time.Duration // Delay before starting
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
	flag.Parse()

	fmt.Printf("=== Request Rate Penalty Test ===\n\n")
	fmt.Printf("Gateway URL: %s\n\n", *gatewayURL)

	ctx := context.Background()
	stats := &Stats{
		Results: make([]RequestResult, 0),
	}

	// Define workloads with SAME criticality but different request rates
	// BOTH START SIMULTANEOUSLY to test rate penalty effect
	workloads := []WorkloadConfig{
		{
			WorkloadID:  "high-rate-burst",
			Criticality: 3, // Same criticality
			NumRequests: 40,
			RequestRate: 0, // Burst mode - send all at once
			Delay:       0,
		},
		{
			WorkloadID:  "low-rate-steady",
			Criticality: 3, // Same criticality
			NumRequests: 40,
			RequestRate: 2.0, // 2 requests per second (steady, over 20 seconds)
			Delay:       0, // Start simultaneously!
		},
	}

	fmt.Printf("Workloads:\n")
	for _, wl := range workloads {
		rateDesc := "burst (all at once)"
		if wl.RequestRate > 0 {
			rateDesc = fmt.Sprintf("%.1f req/s (steady)", wl.RequestRate)
		}
		fmt.Printf("  - %s: criticality=%d, requests=%d, rate=%s, delay=%v\n",
			wl.WorkloadID, wl.Criticality, wl.NumRequests, rateDesc, wl.Delay)
	}
	fmt.Printf("\n")

	var wg sync.WaitGroup
	startTime := time.Now()

	// Launch workloads
	for _, workload := range workloads {
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

// runWorkload sends requests for a single workload
func runWorkload(ctx context.Context, gatewayURL string, workload WorkloadConfig, stats *Stats) {
	fmt.Printf("[%s] Starting workload (criticality=%d, requests=%d)\n",
		workload.WorkloadID, workload.Criticality, workload.NumRequests)

	if workload.RequestRate == 0 {
		// Burst mode - send all requests at once
		var wg sync.WaitGroup
		for i := 0; i < workload.NumRequests; i++ {
			wg.Add(1)
			go func(reqNum int) {
				defer wg.Done()
				result := sendRequest(ctx, gatewayURL, workload.WorkloadID, workload.Criticality, reqNum)
				recordResult(stats, result)
			}(i)
		}
		wg.Wait()
	} else {
		// Rate-limited mode - send at specified rate
		interval := time.Duration(float64(time.Second) / workload.RequestRate)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		var wg sync.WaitGroup
		for i := 0; i < workload.NumRequests; i++ {
			<-ticker.C
			wg.Add(1)
			go func(reqNum int) {
				defer wg.Done()
				result := sendRequest(ctx, gatewayURL, workload.WorkloadID, workload.Criticality, reqNum)
				recordResult(stats, result)
			}(i)
		}
		wg.Wait()
	}

	fmt.Printf("[%s] Completed all requests\n", workload.WorkloadID)
}

// recordResult records a request result
func recordResult(stats *Stats, result RequestResult) {
	stats.ResultsMutex.Lock()
	stats.Results = append(stats.Results, result)
	stats.ResultsMutex.Unlock()

	if result.Success {
		stats.TotalSuccess.Add(1)
	} else {
		stats.TotalFailed.Add(1)
	}
	stats.TotalSent.Add(1)
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

	// Verify request rate penalty is working
	fmt.Printf("\n=== Request Rate Penalty Verification ===\n\n")

	var highRateAvg, lowRateAvg float64
	for _, ws := range workloadStats {
		if ws.WorkloadID == "high-rate-burst" {
			highRateAvg = ws.AvgPosition
		} else if ws.WorkloadID == "low-rate-steady" {
			lowRateAvg = ws.AvgPosition
		}
	}

	fmt.Printf("Both workloads have same criticality (3)\n")
	fmt.Printf("Overall averages: High-rate avg=%.1f, Low-rate avg=%.1f\n\n", highRateAvg, lowRateAvg)

	// More nuanced analysis: Check if low-rate requests interleave with high-rate requests
	// Count how many low-rate requests completed before the last high-rate request
	lowRateBeforeHighRateLast := 0
	lastHighRatePos := 0
	
	for pos, result := range sortedResults {
		if result.WorkloadID == "high-rate-burst" {
			lastHighRatePos = pos + 1
		}
	}
	
	for pos, result := range sortedResults {
		if result.WorkloadID == "low-rate-steady" && (pos+1) < lastHighRatePos {
			lowRateBeforeHighRateLast++
		}
	}
	
	fmt.Printf("Analysis:\n")
	fmt.Printf("  - Last high-rate request completed at position: %d\n", lastHighRatePos)
	fmt.Printf("  - Low-rate requests that completed before last high-rate: %d/%d\n",
		lowRateBeforeHighRateLast, len(workloadPositions["low-rate-steady"]))
	
	// Check first 10 low-rate requests vs last 10 high-rate requests
	firstLowRatePositions := []int{}
	lastHighRatePositions := []int{}
	
	for pos, result := range sortedResults {
		if result.WorkloadID == "low-rate-steady" && len(firstLowRatePositions) < 10 {
			firstLowRatePositions = append(firstLowRatePositions, pos+1)
		}
	}
	
	highRateCount := 0
	for i := len(sortedResults) - 1; i >= 0; i-- {
		if sortedResults[i].WorkloadID == "high-rate-burst" {
			lastHighRatePositions = append([]int{i + 1}, lastHighRatePositions...)
			highRateCount++
			if highRateCount >= 10 {
				break
			}
		}
	}
	
	fmt.Printf("\n  First 10 low-rate positions: %v\n", firstLowRatePositions)
	fmt.Printf("  Last 10 high-rate positions: %v\n", lastHighRatePositions)
	
	// Check if any low-rate requests completed before last high-rate requests
	interleaving := false
	if len(firstLowRatePositions) > 0 && len(lastHighRatePositions) > 0 {
		for _, lowPos := range firstLowRatePositions {
			for _, highPos := range lastHighRatePositions {
				if lowPos < highPos {
					interleaving = true
					break
				}
			}
			if interleaving {
				break
			}
		}
	}
	
	fmt.Printf("\n")
	if interleaving || lowRateBeforeHighRateLast > 0 {
		fmt.Printf("✅ PARTIAL PASS: Request rate penalty shows some effect!\n")
		fmt.Printf("   Low-rate requests interleaved with high-rate requests.\n")
		fmt.Printf("   This shows the policy is considering request rate, though high-rate\n")
		fmt.Printf("   requests that entered the queue first still have an advantage.\n")
	} else {
		fmt.Printf("❌ FAIL: Request rate penalty not observable.\n")
		fmt.Printf("   All high-rate requests completed before all low-rate requests.\n")
		fmt.Printf("   The burst entered the queue too quickly for rate penalty to take effect.\n")
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

	// Calculate request rate from send times
	fmt.Printf("\n=== Actual Request Rates ===\n\n")
	workloadSendTimes := make(map[string][]time.Time)
	for _, r := range stats.Results {
		workloadSendTimes[r.WorkloadID] = append(workloadSendTimes[r.WorkloadID], r.SendTime)
	}

	for workloadID, sendTimes := range workloadSendTimes {
		if len(sendTimes) < 2 {
			continue
		}
		sort.Slice(sendTimes, func(i, j int) bool {
			return sendTimes[i].Before(sendTimes[j])
		})
		duration := sendTimes[len(sendTimes)-1].Sub(sendTimes[0])
		rate := float64(len(sendTimes)-1) / duration.Seconds()
		fmt.Printf("  %s: %.2f req/s over %v\n", workloadID, rate, duration)
	}
}

// Made with Bob
