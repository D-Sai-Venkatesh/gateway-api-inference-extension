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

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

var (
	// Random words for generating test prompts
	words = []string{
		"hello", "world", "test", "inference", "model", "gateway", "kubernetes",
		"cloud", "server", "request", "response", "data", "processing", "compute",
		"network", "service", "endpoint", "traffic", "load", "performance",
		"latency", "throughput", "concurrent", "parallel", "distributed",
		"system", "architecture", "design", "implementation", "optimization",
	}
)

// RequestPayload represents the inference request structure
type RequestPayload struct {
	Model     string `json:"model"`
	Prompt    string `json:"prompt"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

// ProgramContext represents the program context header
type ProgramContext struct {
	ProgramID string `json:"program_id"`
	Hints     Hints  `json:"hints"`
}

// Hints represents the hints in the program context
type Hints struct {
	Criticality int `json:"criticality"`
}

// Stats tracks request statistics
type Stats struct {
	totalRequests   atomic.Int64
	successRequests atomic.Int64
	failedRequests  atomic.Int64
	totalLatency    atomic.Int64 // in milliseconds
}

func main() {
	// Command line flags
	url := flag.String("url", "http://localhost:8080/v1/completions", "Target URL for requests")
	model := flag.String("model", "test-model", "Model name to use in requests")
	minWords := flag.Int("min-words", 5, "Minimum number of words in prompt")
	maxWords := flag.Int("max-words", 20, "Maximum number of words in prompt")
	maxTokens := flag.Int("max-tokens", 100, "Maximum tokens for response")
	timeout := flag.Duration("timeout", 30*time.Second, "Request timeout")

	// Workload mode flags
	numWorkloads := flag.Int("num-workloads", 5, "Number of workloads")
	requestsPerWorkload := flag.Int("requests-per-workload", 20, "Number of requests per workload")
	parallelPerWorkload := flag.Int("parallel-per-workload", 5, "Number of parallel requests per workload")

	flag.Parse()

	stats := &Stats{}
	startTime := time.Now()

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: *timeout,
	}

	log.Printf("Starting load test")
	log.Printf("Number of workloads: %d", *numWorkloads)
	log.Printf("Requests per workload: %d", *requestsPerWorkload)
	log.Printf("Parallel requests per workload: %d", *parallelPerWorkload)
	log.Printf("Total requests: %d", *numWorkloads**requestsPerWorkload)
	log.Printf("Target URL: %s", *url)
	log.Printf("Model: %s", *model)

	runWorkloadMode(client, *url, *model, *minWords, *maxWords, *maxTokens,
		*numWorkloads, *requestsPerWorkload, *parallelPerWorkload, stats)

	duration := time.Since(startTime)

	// Print statistics
	printStats(stats, duration)
}

// runWorkloadMode runs the load test in workload mode
func runWorkloadMode(client *http.Client, url, model string, minWords, maxWords, maxTokens,
	numWorkloads, requestsPerWorkload, parallelPerWorkload int, stats *Stats) {

	var wg sync.WaitGroup

	// Create workloads
	for w := 0; w < numWorkloads; w++ {
		wg.Add(1)

		go func(workloadNum int) {
			defer wg.Done()

			// Generate unique program ID
			programID := fmt.Sprintf("program-%s", uuid.New().String())
			log.Printf("Workload #%d: Starting with program ID: %s", workloadNum, programID)

			// Create program context
			programCtx := &ProgramContext{
				ProgramID: programID,
			}

			// Run requests for this workload
			runWorkloadRequests(client, url, model, minWords, maxWords, maxTokens,
				requestsPerWorkload, parallelPerWorkload, programCtx, stats)

			log.Printf("Workload #%d: Completed all requests", workloadNum)
		}(w + 1)
	}

	// Wait for all workloads to complete
	wg.Wait()
}

// runWorkloadRequests runs requests for a single workload
func runWorkloadRequests(client *http.Client, url, model string, minWords, maxWords, maxTokens,
	requestsPerWorkload, parallelPerWorkload int, programCtx *ProgramContext, stats *Stats) {

	// Create a semaphore to limit parallelism within this workload
	sem := make(chan struct{}, parallelPerWorkload)
	var wg sync.WaitGroup

	for i := 0; i < requestsPerWorkload; i++ {
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore

		go func(requestNum int) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore

			// Generate random prompt
			prompt := generateRandomPrompt(minWords, maxWords)

			// Create request payload
			payload := RequestPayload{
				Model:     model,
				Prompt:    prompt,
				MaxTokens: maxTokens,
			}

			// Set random criticality (1-5) for each request
			reqCtx := &ProgramContext{
				ProgramID: programCtx.ProgramID,
				Hints:     Hints{Criticality: rand.Intn(5) + 1},
			}

			// Send request with program context
			sendRequest(client, url, payload, stats, reqCtx)
		}(i + 1)
	}

	// Wait for all requests in this workload to complete
	wg.Wait()
}

// generateRandomPrompt creates a random prompt with the specified word count range
func generateRandomPrompt(minWords, maxWords int) string {
	numWords := minWords + rand.Intn(maxWords-minWords+1)
	prompt := make([]string, numWords)

	for i := 0; i < numWords; i++ {
		prompt[i] = words[rand.Intn(len(words))]
	}

	return fmt.Sprintf("Generate a response for: %s", joinWords(prompt))
}

// joinWords joins words with spaces
func joinWords(words []string) string {
	result := ""
	for i, word := range words {
		if i > 0 {
			result += " "
		}
		result += word
	}
	return result
}

// sendRequest sends a single HTTP request and updates stats
func sendRequest(client *http.Client, url string, payload RequestPayload, stats *Stats,
	programCtx *ProgramContext) {

	stats.totalRequests.Add(1)

	// Marshal payload to JSON
	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Program %s: Failed to marshal JSON: %v", programCtx.ProgramID, err)
		stats.failedRequests.Add(1)
		return
	}

	// Create request
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Program %s: Failed to create request: %v", programCtx.ProgramID, err)
		stats.failedRequests.Add(1)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	// Add program context header
	programCtxJSON, err := json.Marshal(programCtx)
	if err != nil {
		log.Printf("Program %s: Failed to marshal program context: %v", programCtx.ProgramID, err)
		stats.failedRequests.Add(1)
		return
	}
	req.Header.Set("x-program-context", string(programCtxJSON))

	// Send request and measure latency
	startTime := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(startTime)

	if err != nil {
		log.Printf("Program %s: Failed to send request: %v", programCtx.ProgramID, err)
		stats.failedRequests.Add(1)
		return
	}
	defer resp.Body.Close()

	// Update stats
	stats.totalLatency.Add(latency.Milliseconds())

	// Print response headers
	log.Printf("Program %s (criticality=%d): Response Headers:",
		programCtx.ProgramID, programCtx.Hints.Criticality)
	for key, values := range resp.Header {
		for _, value := range values {
			log.Printf("  %s: %s", key, value)
		}
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		stats.successRequests.Add(1)
		log.Printf("Program %s: Success (Status: %d, Latency: %v, Criticality: %d)",
			programCtx.ProgramID, resp.StatusCode, latency, programCtx.Hints.Criticality)
	} else {
		stats.failedRequests.Add(1)
		log.Printf("Program %s: Failed (Status: %d, Latency: %v, Criticality: %d)",
			programCtx.ProgramID, resp.StatusCode, latency, programCtx.Hints.Criticality)
	}
}

// printStats prints the final statistics
func printStats(stats *Stats, duration time.Duration) {
	total := stats.totalRequests.Load()
	success := stats.successRequests.Load()
	failed := stats.failedRequests.Load()
	totalLatency := stats.totalLatency.Load()

	separator := strings.Repeat("=", 60)
	fmt.Println("\n" + separator)
	fmt.Println("Load Test Results")
	fmt.Println(separator)
	fmt.Printf("Total Requests:     %d\n", total)
	fmt.Printf("Successful:         %d (%.2f%%)\n", success, float64(success)/float64(total)*100)
	fmt.Printf("Failed:             %d (%.2f%%)\n", failed, float64(failed)/float64(total)*100)
	fmt.Printf("Total Duration:     %v\n", duration)
	fmt.Printf("Requests/sec:       %.2f\n", float64(total)/duration.Seconds())

	if success > 0 {
		avgLatency := float64(totalLatency) / float64(success)
		fmt.Printf("Avg Latency:        %.2f ms\n", avgLatency)
	}
	fmt.Println(separator)
}

// Made with Bob
