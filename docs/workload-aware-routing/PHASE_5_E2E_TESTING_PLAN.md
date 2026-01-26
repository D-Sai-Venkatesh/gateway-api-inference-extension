# Phase 5: End-to-End Testing Plan - Integrated with Existing E2E Framework

## Overview
This phase validates the workload-aware routing implementation by integrating tests into the existing e2e test framework (`make test-e2e`). We'll add new Ginkgo test cases to verify workload-aware behavior alongside existing traffic routing tests.

---

## Part 1: Update Configuration for Workload-Aware Testing

### Step 1.1: Create Workload-Aware Test ConfigMap

Create `test/testdata/workload-aware-config.yaml`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: workload-aware-plugins-config
  namespace: $E2E_NS
data:
  workload-aware-plugins.yaml: |
    apiVersion: inference.networking.x-k8s.io/v1alpha1
    kind: EndpointPickerConfig
    plugins:
    - type: workload-aware-ordering-policy
      name: workload-aware-ordering-policy
    - type: queue-scorer
    - type: kv-cache-utilization-scorer
    - type: prefix-cache-scorer
    schedulingProfiles:
    - name: default
      plugins:
      - pluginRef: queue-scorer
      - pluginRef: kv-cache-utilization-scorer
      - pluginRef: prefix-cache-scorer
    featureGates:
    - flowControl
    flowControl:
      registry:
        initialShardCount: 1
        flowGCTimeout: 5m
        priorityBandGCTimeout: 10m
        defaultPriorityBand:
          priority: 0
          priorityName: "Default"
          orderingPolicy: workload-aware-ordering-policy
          queue: ListQueue
          maxBytes: 1000000000
        priorityBands:
          - priority: 5
            priorityName: "Critical"
            orderingPolicy: workload-aware-ordering-policy
            queue: ListQueue
            maxBytes: 2000000000
          - priority: 3
            priorityName: "High"
            orderingPolicy: workload-aware-ordering-policy
            queue: ListQueue
            maxBytes: 1500000000
          - priority: 1
            priorityName: "Low"
            orderingPolicy: workload-aware-ordering-policy
            queue: ListQueue
            maxBytes: 500000000
```

### Step 1.2: Add Workload-Aware Deployment Variant

Add to `test/testdata/inferencepool-e2e.yaml` (or create separate file):

```yaml
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: vllm-llama3-8b-instruct-epp-workload-aware
  namespace: $E2E_NS
  labels:
    app: vllm-llama3-8b-instruct-epp-workload-aware
spec:
  replicas: 1
  selector:
    matchLabels:
      app: vllm-llama3-8b-instruct-epp-workload-aware
  template:
    metadata:
      labels:
        app: vllm-llama3-8b-instruct-epp-workload-aware
    spec:
      serviceAccountName: vllm-llama3-8b-instruct-epp
      terminationGracePeriodSeconds: 130
      containers:
      - name: epp
        image: $E2E_IMAGE
        imagePullPolicy: IfNotPresent
        args:
        - --pool-name
        - "vllm-llama3-8b-instruct"
        - --pool-namespace
        - "$E2E_NS"
        - --v
        - "4"
        - --zap-encoder
        - "json"
        - --grpc-port
        - "9002"
        - --grpc-health-port
        - "9003"
        - "--config-file"
        - "/config/workload-aware-plugins.yaml"
        ports:
        - containerPort: 9002
        - containerPort: 9003
        - name: metrics
          containerPort: 9090
        livenessProbe:
          grpc:
            port: 9003
            service: inference-extension
          initialDelaySeconds: 5
          periodSeconds: 10
        readinessProbe:
          grpc:
            port: 9003
            service: inference-extension
          initialDelaySeconds: 5
          periodSeconds: 10
        volumeMounts:
        - name: workload-aware-config-volume
          mountPath: "/config"
      volumes:
      - name: workload-aware-config-volume
        configMap:
          name: workload-aware-plugins-config
```

---

## Part 2: Add Workload-Aware E2E Tests

### Step 2.1: Create New Test File

Create `test/e2e/epp/workload_aware_test.go`:

```go
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

package epp

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	testutils "sigs.k8s.io/gateway-api-inference-extension/test/utils"
)

const (
	workloadContextHeader = "X-Workload-Context"
)

var _ = ginkgo.Describe("Workload-Aware Routing", func() {
	ginkgo.BeforeEach(func() {
		ginkgo.By("Ensuring workload-aware configuration is loaded")
		// Configuration should be loaded via ConfigMap
	})

	ginkgo.When("Workload-aware routing is enabled", func() {
		ginkgo.It("Should prioritize high-criticality requests over low-criticality requests", func() {
			verifyPriorityOrdering()
		})

		ginkgo.It("Should handle requests without workload context headers gracefully", func() {
			verifyDefaultWorkload()
		})

		ginkgo.It("Should handle invalid workload context gracefully", func() {
			verifyInvalidWorkloadContext()
		})

		ginkgo.It("Should apply request rate fairness across workloads", func() {
			verifyRequestRateFairness()
		})

		ginkgo.It("Should expose workload-aware metrics", func() {
			verifyWorkloadMetrics()
		})
	})
})

// verifyPriorityOrdering tests that high-criticality requests are prioritized
func verifyPriorityOrdering() {
	ginkgo.By("Sending low-priority and high-priority requests")
	
	// Send low-priority request
	lowPriorityCmd := getCurlCommandWithWorkload(
		envoyName, testConfig.NsName, envoyPort, modelName, curlTimeout,
		"/chat/completions",
		[]map[string]any{{"role": "user", "content": "Low priority task"}},
		false,
		`{"workload_id":"low-priority","criticality":1}`,
	)
	
	// Send high-priority request
	highPriorityCmd := getCurlCommandWithWorkload(
		envoyName, testConfig.NsName, envoyPort, modelName, curlTimeout,
		"/chat/completions",
		[]map[string]any{{"role": "user", "content": "High priority task"}},
		false,
		`{"workload_id":"high-priority","criticality":5}`,
	)
	
	// Execute both requests
	var wg sync.WaitGroup
	results := make(chan string, 2)
	
	wg.Add(2)
	go func() {
		defer wg.Done()
		resp, err := testutils.ExecCommandInPod(testConfig, "curl", "curl", lowPriorityCmd)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		results <- resp
	}()
	
	time.Sleep(100 * time.Millisecond) // Ensure low-priority arrives first
	
	go func() {
		defer wg.Done()
		resp, err := testutils.ExecCommandInPod(testConfig, "curl", "curl", highPriorityCmd)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		results <- resp
	}()
	
	wg.Wait()
	close(results)
	
	// Verify both requests succeeded
	successCount := 0
	for resp := range results {
		if strings.Contains(resp, "200 OK") {
			successCount++
		}
	}
	gomega.Expect(successCount).To(gomega.Equal(2), "Both requests should succeed")
}

// verifyDefaultWorkload tests handling of requests without workload context
func verifyDefaultWorkload() {
	ginkgo.By("Sending request without workload context header")
	
	curlCmd := getCurlCommand(
		envoyName, testConfig.NsName, envoyPort, modelName, curlTimeout,
		"/chat/completions",
		[]map[string]any{{"role": "user", "content": "No workload context"}},
		false,
	)
	
	resp, err := testutils.ExecCommandInPod(testConfig, "curl", "curl", curlCmd)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(resp).To(gomega.ContainSubstring("200 OK"), "Request should succeed with default workload")
}

// verifyInvalidWorkloadContext tests handling of malformed workload context
func verifyInvalidWorkloadContext() {
	ginkgo.By("Sending requests with invalid workload context")
	
	testCases := []struct {
		name    string
		context string
	}{
		{"invalid JSON", `{invalid-json}`},
		{"missing criticality", `{"workload_id":"test"}`},
		{"out of range criticality", `{"workload_id":"test","criticality":10}`},
		{"negative criticality", `{"workload_id":"test","criticality":-5}`},
		{"null workload_id", `{"workload_id":null,"criticality":3}`},
	}
	
	for _, tc := range testCases {
		ginkgo.By(fmt.Sprintf("Testing %s", tc.name))
		
		curlCmd := getCurlCommandWithWorkload(
			envoyName, testConfig.NsName, envoyPort, modelName, curlTimeout,
			"/chat/completions",
			[]map[string]any{{"role": "user", "content": tc.name}},
			false,
			tc.context,
		)
		
		resp, err := testutils.ExecCommandInPod(testConfig, "curl", "curl", curlCmd)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(resp).To(gomega.ContainSubstring("200 OK"), 
			fmt.Sprintf("Request with %s should succeed with defaults", tc.name))
	}
}

// verifyRequestRateFairness tests fairness across workloads with different request rates
func verifyRequestRateFairness() {
	ginkgo.By("Sending requests from multiple workloads with different rates")
	
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, maxConcurrentRequests)
	
	// High-rate workload (20 requests)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		semaphore <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-semaphore }()
			
			curlCmd := getCurlCommandWithWorkload(
				envoyName, testConfig.NsName, envoyPort, modelName, curlTimeout,
				"/chat/completions",
				[]map[string]any{{"role": "user", "content": fmt.Sprintf("High rate %d", idx)}},
				false,
				`{"workload_id":"high-rate","criticality":3}`,
			)
			
			_, err := testutils.ExecCommandInPod(testConfig, "curl", "curl", curlCmd)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}(i)
	}
	
	// Low-rate workload (3 requests)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		semaphore <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-semaphore }()
			
			curlCmd := getCurlCommandWithWorkload(
				envoyName, testConfig.NsName, envoyPort, modelName, curlTimeout,
				"/chat/completions",
				[]map[string]any{{"role": "user", "content": fmt.Sprintf("Low rate %d", idx)}},
				false,
				`{"workload_id":"low-rate","criticality":3}`,
			)
			
			_, err := testutils.ExecCommandInPod(testConfig, "curl", "curl", curlCmd)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}(i)
	}
	
	wg.Wait()
	ginkgo.By("All requests completed successfully")
}

// verifyWorkloadMetrics checks that workload-aware metrics are exposed
func verifyWorkloadMetrics() {
	ginkgo.By("Generating traffic with workload context")
	
	// Send requests from different workloads
	workloads := []struct {
		id          string
		criticality int
	}{
		{"metrics-test-1", 5},
		{"metrics-test-2", 3},
		{"metrics-test-3", 1},
	}
	
	for _, w := range workloads {
		curlCmd := getCurlCommandWithWorkload(
			envoyName, testConfig.NsName, envoyPort, modelName, curlTimeout,
			"/chat/completions",
			[]map[string]any{{"role": "user", "content": "Metrics test"}},
			false,
			fmt.Sprintf(`{"workload_id":"%s","criticality":%d}`, w.id, w.criticality),
		)
		
		_, err := testutils.ExecCommandInPod(testConfig, "curl", "curl", curlCmd)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}
	
	ginkgo.By("Scraping metrics and verifying workload metrics exist")
	podIP := findReadyPod().Status.PodIP
	token, err := getMetricsReaderToken(testConfig.K8sClient)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	
	metricScrapeCmd := getMetricsScrapeCommand(podIP, token)
	
	gomega.Eventually(func() error {
		resp, err := testutils.ExecCommandInPod(testConfig, "curl", "curl", metricScrapeCmd)
		if err != nil {
			return err
		}
		
		if !strings.Contains(resp, "200 OK") {
			return fmt.Errorf("did not get 200 OK: %s", resp)
		}
		
		// Check for workload-aware metrics
		expectedMetrics := []string{
			"workload_active_requests",
			"workload_total_requests",
			"workload_request_rate",
		}
		
		for _, metric := range expectedMetrics {
			if !strings.Contains(resp, metric) {
				return fmt.Errorf("expected metric %s not found", metric)
			}
		}
		
		return nil
	}, testConfig.ReadyTimeout, testConfig.Interval).Should(gomega.Succeed())
}

// getCurlCommandWithWorkload extends getCurlCommand to include workload context header
func getCurlCommandWithWorkload(name, ns, port, model string, timeout time.Duration, api string, promptOrMessages any, streaming bool, workloadContext string) []string {
	baseCmd := getCurlCommand(name, ns, port, model, timeout, api, promptOrMessages, streaming)
	
	// Insert workload context header before the body (-d parameter)
	// Find the -d parameter and insert headers before it
	for i, arg := range baseCmd {
		if arg == "-d" {
			// Insert workload context header before -d
			result := make([]string, 0, len(baseCmd)+2)
			result = append(result, baseCmd[:i]...)
			result = append(result, "-H", fmt.Sprintf("%s: %s", workloadContextHeader, workloadContext))
			result = append(result, baseCmd[i:]...)
			return result
		}
	}
	
	// If -d not found, append at the end
	return append(baseCmd, "-H", fmt.Sprintf("%s: %s", workloadContextHeader, workloadContext))
}
```

---

## Part 3: Execution Plan

### Step 3.1: Build and Test

```bash
# 1. Build the EPP image with workload-aware changes
make image-build

# 2. Run e2e tests (will use existing test-e2e target)
make test-e2e

# 3. Run only workload-aware tests (if needed)
go test ./test/e2e/epp/ -v -ginkgo.v -ginkgo.focus="Workload-Aware"
```

### Step 3.2: Verify Test Coverage

The new tests will verify:
- ✅ Priority ordering (high-criticality requests prioritized)
- ✅ Default workload handling (missing headers)
- ✅ Invalid input handling (malformed JSON, out-of-range values)
- ✅ Request rate fairness (multiple workloads)
- ✅ Metrics exposure (workload-aware metrics)

### Step 3.3: Integration with CI/CD

The tests integrate seamlessly with existing CI/CD:
- Uses same `make test-e2e` target
- Follows same Ginkgo test patterns
- Uses same test utilities and helpers
- Respects same environment variables (`E2E_NS`, `E2E_IMAGE`, etc.)

---

## Part 4: Manual Testing Scenarios (Optional)

For additional manual verification beyond automated tests:

### Scenario 1: Interactive Priority Testing

```bash
# Terminal 1: Watch EPP logs
kubectl logs -f -n inf-ext-e2e -l app=vllm-llama3-8b-instruct-epp

# Terminal 2: Send low-priority request
kubectl exec -n inf-ext-e2e curl -- curl -X POST \
  http://envoy.inf-ext-e2e.svc:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Workload-Context: {\"workload_id\":\"manual-low\",\"criticality\":1}" \
  -d '{"model":"llama-2","messages":[{"role":"user","content":"Low priority"}]}'

# Terminal 3: Send high-priority request (should jump queue)
kubectl exec -n inf-ext-e2e curl -- curl -X POST \
  http://envoy.inf-ext-e2e.svc:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Workload-Context: {\"workload_id\":\"manual-high\",\"criticality\":5}" \
  -d '{"model":"llama-2","messages":[{"role":"user","content":"High priority"}]}'
```

### Scenario 2: Metrics Verification

```bash
# Port-forward to EPP metrics endpoint
kubectl port-forward -n inf-ext-e2e svc/vllm-llama3-8b-instruct-epp 9090:9090

# Query metrics
curl http://localhost:9090/metrics | grep workload

# Expected output:
# workload_active_requests{workload_id="manual-low"} 0
# workload_total_requests{workload_id="manual-low"} 1
# workload_request_rate{workload_id="manual-low"} 0.016
```

---

## Part 5: Validation Checklist

### Automated Test Coverage
- [ ] Priority ordering test passes
- [ ] Default workload test passes
- [ ] Invalid input handling test passes
- [ ] Request rate fairness test passes
- [ ] Workload metrics test passes
- [ ] All existing e2e tests still pass

### Manual Verification (Optional)
- [ ] High-criticality requests prioritized in logs
- [ ] Default workload assigned for missing headers
- [ ] Invalid contexts handled gracefully
- [ ] Workload metrics exposed correctly
- [ ] No crashes or panics under load

### Integration
- [ ] Tests run via `make test-e2e`
- [ ] Tests follow Ginkgo patterns
- [ ] Tests use existing test utilities
- [ ] Tests respect environment variables
- [ ] Tests clean up resources properly

---

## Part 6: Success Criteria

Phase 5 is complete when:

1. ✅ All new workload-aware e2e tests pass
2. ✅ All existing e2e tests continue to pass
3. ✅ Tests integrate with `make test-e2e` target
4. ✅ Workload-aware metrics are exposed and accurate
5. ✅ No regressions in existing functionality
6. ✅ Tests are documented and maintainable
7. ✅ CI/CD pipeline includes workload-aware tests

---

## Part 7: Next Steps

After Phase 5 completion:
- Document any issues found and fixes applied
- Update test documentation in `test/e2e/epp/README.md`
- Proceed to **Phase 6: Performance & Load Testing**
- Consider adding more edge case tests based on findings
- Update conformance tests if needed

---

## Troubleshooting

### Issue: Tests fail to find workload-aware plugin
**Solution:** Verify plugin is registered in `pkg/epp/flowcontrol/framework/plugins/intraflow/plugin.go`

### Issue: Workload context not extracted
**Solution:** Check header name matches `X-Workload-Context` and JSON is valid

### Issue: Metrics not appearing
**Solution:** Ensure flowControl feature gate is enabled in config

### Issue: Tests timeout
**Solution:** Increase `curlTimeout` or check EPP pod logs for errors