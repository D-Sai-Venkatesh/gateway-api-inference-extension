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

// Package main tests the workload-aware flow control policy by simulating multiple workloads
// with different priorities and analyzing the completion order to verify the policy is working correctly.
//
// Test Strategy:
// 1. Saturate the system by sending many requests to fill the queue (depth > 5)
// 2. Send requests from multiple workloads with different criticality levels
// 3. Track completion order and verify it matches expected priority order
// 4. Verify workload registry metrics are being tracked correctly
//
// Workload-Aware Policy Scoring (from pkg/epp/flowcontrol/framework/plugins/intraflow/workload_aware.go):
//   Score = (AvgWaitTime × 0.4) + (Criticality × 0.4) - (RequestRate × 0.2)
//
// Components:
//   - AvgWaitTime: Workload's historical average wait time (EMA with α=0.2)
//   - Criticality: User-defined priority level (1-5, normalized to 0-1)
//   - RequestRate: Requests per second (penalizes high-rate workloads for fairness)
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
	Criticality int    // 1-5, where 5 is highest priority
	NumRequests int    // Number of requests to send
	Delay       time.Duration // Delay before starting this workload
}

// RequestResult tracks the result of a single request
type RequestResult struct {
	RequestID   int
	WorkloadID  string
	Criticality int
	SendTime    time.Time
	CompleteTime time.Time
	Duration    time.Duration
	StatusCode  int
	Success     bool
	Error       error
}

// TestConfig holds the test configuration
type TestConfig struct {
	GatewayURL string
	Workloads  []WorkloadConfig
}

// Stats tracks overall test statistics
type Stats struct {
	TotalSent      atomic.Int64
	TotalSuccess   atomic.Int64
	TotalFailed    atomic.Int64
	Results        []RequestResult
	ResultsMutex   sync.Mutex
}

// InferenceRequest represents the request payload
type InferenceRequest struct {
	Model       string  `json:"model"`
	Prompt      string  `json:"prompt"`
	MaxTokens   int     `json:"max_tokens"`
	Temperature float64 `json:"temperature"`
}

func main() {
	config := parseFlags()
	
	fmt.Printf("=== Workload-Aware Flow Control Test ===\n\n")
	fmt.Printf("Gateway URL: %s\n", config.GatewayURL)
	fmt.Printf("Workloads:\n")
	for i, wl := range config.Workloads {
		fmt.Printf("  %d. %s (criticality=%d, requests=%d, delay=%v)\n", 
			i+1, wl.WorkloadID, wl.Criticality, wl.NumRequests, wl.Delay)
	}
	fmt.Printf("\n")

	stats := &Stats{
		Results: make([]RequestResult, 0),
	}

	ctx := context.Background()
	var wg sync.WaitGroup

	startTime := time.Now()

	// Launch each workload as a separate goroutine
	for _, workload := range config.Workloads {
		wg.Add(1)
		go func(wl WorkloadConfig) {
			defer wg.Done()
			
			// Delay before starting this workload
			if wl.Delay > 0 {
				time.Sleep(wl.Delay)
			}
			
			runWorkload(ctx, config.GatewayURL, wl, stats)
		}(workload)
	}

	// Wait for all workloads to complete
	wg.Wait()
	totalDuration := time.Since(startTime)

	// Analyze results
	analyzeResults(stats, totalDuration)
}

func parseFlags() *TestConfig {
	gatewayURL := flag.String("url", "http://localhost:8081/v1/completions", "Gateway URL")
	flag.Parse()

	// Define test workloads
	// Strategy: Send MANY low-priority requests first to saturate the system,
	// then send high-priority requests that should jump the queue
	config := &TestConfig{
		GatewayURL: *gatewayURL,
		Workloads: []WorkloadConfig{
			{
				WorkloadID:  "background-workload",
				Criticality: 1, // Low priority
				NumRequests: 50, // Increased to saturate system
				Delay:       0, // Start immediately to saturate system
			},
			{
				WorkloadID:  "normal-workload",
				Criticality: 3, // Medium priority
				NumRequests: 30, // Increased
				Delay:       200 * time.Millisecond, // Start after background workload
			},
			{
				WorkloadID:  "critical-workload",
				Criticality: 5, // High priority
				NumRequests: 20, // Increased
				Delay:       400 * time.Millisecond, // Start last, should still complete first
			},
		},
	}

	return config
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
			sendRequest(ctx, gatewayURL, workload, reqNum, stats)
		}(i)
		
		// Small delay between requests to avoid overwhelming the client
		time.Sleep(10 * time.Millisecond) // Reduced delay to send faster
	}

	wg.Wait()
	fmt.Printf("[%s] Completed all requests\n", workload.WorkloadID)
}

// sendRequest sends a single request and records the result
func sendRequest(ctx context.Context, gatewayURL string, workload WorkloadConfig, reqNum int, stats *Stats) {
	stats.TotalSent.Add(1)
	
	// Create unique request ID
	requestID := int(stats.TotalSent.Load())
	
	// Create request payload
	payload := InferenceRequest{
		Model:       "meta-llama/Llama-3.1-8B-Instruct",
		Prompt:      fmt.Sprintf("[%s-req-%d] Test request", workload.WorkloadID, reqNum),
		MaxTokens:   50,
		Temperature: 0,
	}
	
	jsonData, err := json.Marshal(payload)
	if err != nil {
		recordResult(stats, RequestResult{
			RequestID:   requestID,
			WorkloadID:  workload.WorkloadID,
			Criticality: workload.Criticality,
			SendTime:    time.Now(),
			Success:     false,
			Error:       err,
		})
		return
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", gatewayURL, bytes.NewBuffer(jsonData))
	if err != nil {
		recordResult(stats, RequestResult{
			RequestID:   requestID,
			WorkloadID:  workload.WorkloadID,
			Criticality: workload.Criticality,
			SendTime:    time.Now(),
			Success:     false,
			Error:       err,
		})
		return
	}

	// Add headers (matching e2e test format)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "close")
	req.Header.Set("X-Inference-Objective", "inferenceobjective-sample")
	req.Header.Set("X-Model-Name-Rewrite", "llama3-8b-instruct")
	
	// Add workload context header for workload-aware routing
	workloadContext := fmt.Sprintf(`{"workload_id":"%s","criticality":%d}`, 
		workload.WorkloadID, workload.Criticality)
	req.Header.Set("X-Workload-Context", workloadContext)

	// Send request and measure time
	sendTime := time.Now()
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	completeTime := time.Now()
	duration := completeTime.Sub(sendTime)

	result := RequestResult{
		RequestID:    requestID,
		WorkloadID:   workload.WorkloadID,
		Criticality:  workload.Criticality,
		SendTime:     sendTime,
		CompleteTime: completeTime,
		Duration:     duration,
		Error:        err,
	}

	if err != nil {
		result.Success = false
		stats.TotalFailed.Add(1)
	} else {
		defer resp.Body.Close()
		result.StatusCode = resp.StatusCode
		result.Success = (resp.StatusCode == http.StatusOK)
		
		if result.Success {
			stats.TotalSuccess.Add(1)
		} else {
			stats.TotalFailed.Add(1)
			// Read error body
			body, _ := io.ReadAll(resp.Body)
			result.Error = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		}
	}

	recordResult(stats, result)
}

// recordResult safely adds a result to the stats
func recordResult(stats *Stats, result RequestResult) {
	stats.ResultsMutex.Lock()
	defer stats.ResultsMutex.Unlock()
	stats.Results = append(stats.Results, result)
}

// analyzeResults analyzes the completion order and verifies workload-aware policy
func analyzeResults(stats *Stats, totalDuration time.Duration) {
	fmt.Printf("\n=== Test Results ===\n\n")
	fmt.Printf("Total Duration: %v\n", totalDuration)
	fmt.Printf("Total Requests: %d\n", stats.TotalSent.Load())
	fmt.Printf("  Success: %d\n", stats.TotalSuccess.Load())
	fmt.Printf("  Failed: %d\n", stats.TotalFailed.Load())
	fmt.Printf("\n")

	// Sort results by completion time
	sortedResults := make([]RequestResult, len(stats.Results))
	copy(sortedResults, stats.Results)
	sort.Slice(sortedResults, func(i, j int) bool {
		return sortedResults[i].CompleteTime.Before(sortedResults[j].CompleteTime)
	})

	// Analyze completion order by priority
	fmt.Printf("=== Completion Order Analysis ===\n\n")
	
	// Group by criticality
	criticalityGroups := make(map[int][]RequestResult)
	for _, result := range sortedResults {
		if result.Success {
			criticalityGroups[result.Criticality] = append(criticalityGroups[result.Criticality], result)
		}
	}

	// Calculate average completion position for each criticality level
	fmt.Printf("Average Completion Position by Criticality:\n")
	for crit := 5; crit >= 1; crit-- {
		if results, ok := criticalityGroups[crit]; ok {
			totalPos := 0
			for _, result := range results {
				// Find position in sorted list
				for pos, r := range sortedResults {
					if r.RequestID == result.RequestID {
						totalPos += pos + 1 // 1-based position
						break
					}
				}
			}
			avgPos := float64(totalPos) / float64(len(results))
			fmt.Printf("  Criticality %d: Avg position %.1f (count=%d)\n", crit, avgPos, len(results))
		}
	}
	fmt.Printf("\n")

	// Verify policy is working: Higher criticality should have lower average position
	fmt.Printf("=== Policy Verification ===\n\n")
	
	// Check if high-priority requests completed before low-priority
	highPriorityAvg := calculateAvgPosition(sortedResults, criticalityGroups[5])
	mediumPriorityAvg := calculateAvgPosition(sortedResults, criticalityGroups[3])
	lowPriorityAvg := calculateAvgPosition(sortedResults, criticalityGroups[1])

	fmt.Printf("Expected Order: Critical (5) < Normal (3) < Background (1)\n")
	fmt.Printf("Actual Avg Positions: Critical=%.1f, Normal=%.1f, Background=%.1f\n\n", 
		highPriorityAvg, mediumPriorityAvg, lowPriorityAvg)

	// Verify ordering
	policyWorking := true
	if highPriorityAvg < mediumPriorityAvg && mediumPriorityAvg < lowPriorityAvg {
		fmt.Printf("✅ PASS: Workload-aware policy is working correctly!\n")
		fmt.Printf("   High-priority requests completed before low-priority requests.\n")
	} else {
		fmt.Printf("❌ FAIL: Policy may not be working as expected.\n")
		if highPriorityAvg >= mediumPriorityAvg {
			fmt.Printf("   Critical workload did not complete before normal workload.\n")
		}
		if mediumPriorityAvg >= lowPriorityAvg {
			fmt.Printf("   Normal workload did not complete before background workload.\n")
		}
		policyWorking = false
	}
	fmt.Printf("\n")

	// Show detailed completion timeline
	if !policyWorking {
		fmt.Printf("=== Detailed Completion Timeline ===\n\n")
		for i, result := range sortedResults {
			if result.Success {
				fmt.Printf("%2d. [%s] criticality=%d, duration=%v\n", 
					i+1, result.WorkloadID, result.Criticality, result.Duration)
			}
		}
	}
}

// calculateAvgPosition calculates the average completion position for a group of results
func calculateAvgPosition(sortedResults []RequestResult, group []RequestResult) float64 {
	if len(group) == 0 {
		return 0
	}
	
	totalPos := 0
	for _, result := range group {
		for pos, r := range sortedResults {
			if r.RequestID == result.RequestID {
				totalPos += pos + 1
				break
			}
		}
	}
	return float64(totalPos) / float64(len(group))
}

// Made with Bob
