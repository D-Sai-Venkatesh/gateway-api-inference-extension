# Phase 5: Configuration Explanation

## Issue Resolved

**Error:** `unknown field "flowControl"`

**Root Cause:** The `EndpointPickerConfig` API type does not have a `flowControl` field. Flow control configuration is created **programmatically** by the config loader, not via YAML.

## How Flow Control Configuration Works

### 1. YAML Configuration (User-Provided)
```yaml
apiVersion: inference.networking.x-k8s.io/v1alpha1
kind: EndpointPickerConfig
plugins:
  - type: workload-aware-ordering-policy
    name: workload-aware-ordering-policy
  - type: queue-scorer
  # ... other plugins
schedulingProfiles:
  - name: default
    plugins:
      - pluginRef: queue-scorer
      # ... other plugin refs
featureGates:
  - flowControl  # This enables flow control
```

### 2. Programmatic Configuration (Automatic)

When `flowControl` feature gate is enabled, the config loader automatically:

**File:** [`pkg/epp/config/loader/configloader.go:109-122`](pkg/epp/config/loader/configloader.go:109-122)

```go
if featureGates[flowcontrol.FeatureGate] {
    registryConfig, err := registry.NewConfig(handle)
    if err != nil {
        return nil, fmt.Errorf("flow registry config build failed: %w", err)
    }
    cfg := &flowcontrol.Config{
        Controller: fccontroller.Config{},
        Registry:   registryConfig,
    }
    flowControlConfig, err = cfg.ValidateAndApplyDefaults()
    if err != nil {
        return nil, fmt.Errorf("flow control config build failed: %w", err)
    }
}
```

### 3. Default Registry Configuration

**File:** [`pkg/epp/flowcontrol/registry/config.go:359-403`](pkg/epp/flowcontrol/registry/config.go:359-403)

The `registry.NewConfig(handle)` creates default configuration with:
- **InitialShardCount:** 1
- **FlowGCTimeout:** 5 minutes
- **PriorityBandGCTimeout:** 10 minutes  
- **DefaultPriorityBand:** Uses default ordering policy from handle
- **PriorityBands:** Empty map (bands created dynamically as needed)

## Correct E2E Test Configuration

### File: `test/testdata/inferencepool-e2e.yaml`

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: plugins-config
  namespace: $E2E_NS
data:
  default-plugins.yaml: |
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
```

**Key Points:**
1. ✅ Plugin `workload-aware-ordering-policy` is declared
2. ✅ Feature gate `flowControl` is enabled
3. ✅ NO `flowControl:` configuration section (this was the error)

## How Workload-Aware Routing Works

### 1. Plugin Registration
The `workload-aware-ordering-policy` plugin is registered in:
[`pkg/epp/flowcontrol/framework/plugins/intraflow/plugin.go`](pkg/epp/flowcontrol/framework/plugins/intraflow/plugin.go)

### 2. Default Priority Band Creation
When `registry.NewConfig(handle)` is called:
- It looks up the default ordering policy from the handle
- Since we declared `workload-aware-ordering-policy`, it becomes the default
- All priority bands (default and dynamic) will use this policy

### 3. Registry Injection
In [`cmd/epp/runner/runner.go:212-248`](cmd/epp/runner/runner.go:212-248):
- WorkloadRegistry is injected into all static priority bands
- WorkloadRegistry is injected into DefaultPriorityBand template
- Dynamic bands inherit registry via shared OrderingPolicy pointer

### 4. Request Flow
1. Request arrives with `X-Workload-Context` header
2. Context extracted in [`pkg/epp/handlers/server.go`](pkg/epp/handlers/server.go)
3. Copied to Request.Metadata in [`pkg/epp/handlers/request.go:97-104`](pkg/epp/handlers/request.go:97-104)
4. WorkloadAwarePolicy uses metadata to compute priority score
5. Registry tracks workload statistics (active requests, request rate)
6. Score adjusted based on criticality, wait time, and request rate

## Testing the Configuration

### 1. Build and Deploy
```bash
# Build EPP image
make image-build

# Run e2e tests
make test-e2e
```

### 2. Verify Configuration Loaded
Check EPP logs for:
```
{"level":"info","msg":"Loaded raw configuration","config":"..."}
{"level":"info","msg":"Effective configuration loaded","config":"..."}
```

### 3. Verify Plugin Instantiated
Check logs for:
```
{"level":"info","msg":"Plugin instantiated","name":"workload-aware-ordering-policy","type":"workload-aware-ordering-policy"}
```

### 4. Verify Flow Control Enabled
Check logs for:
```
{"level":"info","msg":"Flow control enabled"}
```

### 5. Send Test Request
```bash
kubectl exec -n inf-ext-e2e curl -- curl -X POST \
  http://envoy.inf-ext-e2e.svc:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Workload-Context: {\"workload_id\":\"test\",\"criticality\":5}" \
  -d '{"model":"llama-2","messages":[{"role":"user","content":"Test"}]}'
```

### 6. Check Workload Context Extraction
Look for logs:
```
{"level":"debug","msg":"workload context extracted","workload_id":"test","criticality":5}
```

### 7. Verify Priority Score Computation
Look for logs:
```
{"level":"debug","msg":"priority score computed","workload_id":"test","score":5.0}
```

## Customizing Flow Control (Future Enhancement)

If you need to customize flow control configuration in the future, you would need to:

1. **Add fields to EndpointPickerConfig API:**
   - Modify [`apix/config/v1alpha1/endpointpickerconfig_types.go`](apix/config/v1alpha1/endpointpickerconfig_types.go)
   - Add `FlowControl *FlowControlConfig` field

2. **Update config loader:**
   - Modify [`pkg/epp/config/loader/configloader.go`](pkg/epp/config/loader/configloader.go)
   - Use YAML config instead of defaults

3. **Regenerate CRDs:**
   ```bash
   make generate
   ```

For now, the default configuration is sufficient for testing workload-aware routing.

## Next Steps

1. ✅ Configuration fixed - remove `flowControl:` section from YAML
2. 🔄 Run `make test-e2e` to verify EPP starts correctly
3. 📝 Add workload-aware test cases (see PHASE_5_E2E_TESTING_PLAN.md)
4. 🧪 Test with X-Workload-Context headers
5. 📊 Verify metrics and logs