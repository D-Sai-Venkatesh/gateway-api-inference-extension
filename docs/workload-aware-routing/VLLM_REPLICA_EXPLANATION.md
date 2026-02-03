# vLLM Replica Configuration Explanation

## Question
"If in test data, if num replicas for vllm sim is 1 then why 3 replicas are there?"

## Answer

There are **two separate deployments** with different replica counts:

### 1. vLLM Model Server Deployment (3 replicas)
**File**: [`config/manifests/vllm/sim-deployment.yaml`](../../config/manifests/vllm/sim-deployment.yaml:6)
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: vllm-llama3-8b-instruct
spec:
  replicas: 3  # <-- 3 vLLM pods
```

This is the **model server** that actually serves inference requests. It has **3 replicas** to simulate a real production environment with multiple backend endpoints.

### 2. EPP (Endpoint Picker) Deployment (1 replica)
**File**: [`test/testdata/inferencepool-e2e.yaml`](../../test/testdata/inferencepool-e2e.yaml:133)
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: vllm-llama3-8b-instruct-epp
spec:
  replicas: 1  # <-- 1 EPP pod
```

This is the **Endpoint Picker (EPP)** that routes requests to the vLLM pods. It has **1 replica** for the default e2e test configuration.

## How They Work Together

```
Client Request
    ↓
[EPP Pod] (1 replica) - Routes requests using workload-aware logic
    ↓
[vLLM Pods] (3 replicas) - Serve inference requests
    ├── vllm-llama3-8b-instruct-0
    ├── vllm-llama3-8b-instruct-1
    └── vllm-llama3-8b-instruct-2
```

## Configuration Sources

The vLLM deployment is created from the manifest specified by the `MANIFEST_PATH` environment variable:

**From [`Makefile`](../../Makefile:8)**:
```makefile
E2E_MANIFEST_PATH ?= config/manifests/vllm/sim-deployment.yaml
```

**From [`test/e2e/epp/e2e_suite_test.go`](../../test/e2e/epp/e2e_suite_test.go:76)**:
```go
modelServerManifestFilepathEnvVar = "MANIFEST_PATH"
```

When you run `make test-e2e`, it sets:
```bash
MANIFEST_PATH=$(PROJECT_DIR)/config/manifests/vllm/sim-deployment.yaml
```

## Why 3 vLLM Replicas?

Having **3 vLLM replicas** is beneficial for testing because:

1. **Realistic Load Balancing**: Tests how EPP distributes load across multiple backends
2. **Workload-Aware Routing**: With multiple endpoints, we can observe how different workloads are routed to different pods based on priority
3. **Saturation Testing**: We can saturate one or more pods while testing priority ordering on others
4. **Failure Scenarios**: Can test behavior when some endpoints are busy/unavailable

## Leader Election Mode

There's also a **leader election mode** that uses 3 EPP replicas:

**File**: [`test/testdata/inferencepool-leader-election-e2e.yaml`](../../test/testdata/inferencepool-leader-election-e2e.yaml)
```yaml
spec:
  replicas: 3  # <-- 3 EPP pods (only 1 active leader)
```

This is enabled by setting `E2E_LEADER_ELECTION_ENABLED=true`.

## Summary

| Component | Replicas | Purpose |
|-----------|----------|---------|
| vLLM Model Server | **3** | Backend inference endpoints |
| EPP (default) | **1** | Request router |
| EPP (leader election) | **3** | Request router with HA |

The confusion arose because there are two different deployments with different replica counts, but they serve different purposes in the architecture.