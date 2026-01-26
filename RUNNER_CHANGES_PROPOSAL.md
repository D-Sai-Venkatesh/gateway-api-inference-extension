# Exact runner.go Changes Needed

## Current Flow Analysis

Based on the code review, here's how configuration flows:

```
1. runner.go:206 → parseConfigurationPhaseTwo(ctx, rawConfig, ds)
   ↓
2. configloader.go:78 → InstantiateAndConfigure(rawConfig, handle, logger)
   ↓
3. configloader.go:110 → registry.NewConfig(handle)  // Creates FlowRegistry config
   ↓
4. configloader.go:114-122 → Creates flowcontrol.Config with Registry
   ↓
5. Returns to runner.go:206 → eppConfig (contains FlowControlConfig)
   ↓
6. runner.go:286 → fcregistry.NewFlowRegistry(eppConfig.FlowControlConfig.Registry, setupLog)
```

## Problem Identified

The `registry.NewConfig(handle)` is called in **configloader.go** (line 110), which:
- Does NOT have access to the datastore
- Cannot inject WorkloadRegistry at this point

## Solution: Inject WorkloadRegistry AFTER Config Loading

We need to inject the WorkloadRegistry in **runner.go** AFTER the config is loaded but BEFORE the FlowRegistry is created.

## Exact Changes Required

### File: `cmd/epp/runner/runner.go`

**Location:** After line 206 (after parseConfigurationPhaseTwo returns)

```go
// CURRENT CODE (lines 200-210):
ds, err := setupDatastore(ctx, epf, int32(opts.ModelServerMetricsPort), startCrdReconcilers,
    opts.PoolName, opts.PoolNamespace, opts.EndpointSelector, opts.EndpointTargetPorts)
if err != nil {
    setupLog.Error(err, "Failed to setup datastore")
    return err
}
eppConfig, err := r.parseConfigurationPhaseTwo(ctx, rawConfig, ds)
if err != nil {
    setupLog.Error(err, "Failed to parse configuration")
    return err
}

// ADD THIS NEW CODE BLOCK (after line 210):
// Inject WorkloadRegistry into FlowControl config if flow control is enabled
if eppConfig.FlowControlConfig != nil {
    workloadRegistry := ds.GetWorkloadRegistry()
    eppConfig.FlowControlConfig.Registry.WorkloadRegistry = workloadRegistry
    setupLog.Info("Injected WorkloadRegistry into FlowControl configuration")
}
```

### Alternative: Modify configloader.go to Accept Datastore

If we want to inject earlier, we could modify the config loader:

**File: `pkg/epp/config/loader/configloader.go`**

```go
// CHANGE function signature (line 78):
func InstantiateAndConfigure(
    rawConfig *configapi.EndpointPickerConfig,
    handle fwkplugin.Handle,
    logger logr.Logger,
    ds datastore.Datastore,  // ADD THIS PARAMETER
) (*config.Config, error) {

// THEN at line 110-122:
var flowControlConfig *flowcontrol.Config
if featureGates[flowcontrol.FeatureGate] {
    registryConfig, err := registry.NewConfig(handle)
    if err != nil {
        return nil, fmt.Errorf("flow registry config build failed: %w", err)
    }
    
    // INJECT WorkloadRegistry here
    if ds != nil {
        registryConfig.WorkloadRegistry = ds.GetWorkloadRegistry()
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

**And update the call in runner.go (line 206):**
```go
eppConfig, err := r.parseConfigurationPhaseTwo(ctx, rawConfig, ds)  // ds already passed
```

**And update parseConfigurationPhaseTwo (line 439):**
```go
func (r *Runner) parseConfigurationPhaseTwo(ctx context.Context, rawConfig *configapi.EndpointPickerConfig, ds datastore.Datastore) (*config.Config, error) {
    logger := log.FromContext(ctx)
    handle := fwkplugin.NewEppHandle(ctx, makePodListFunc(ds))
    cfg, err := loader.InstantiateAndConfigure(rawConfig, handle, logger, ds)  // ADD ds parameter
    // ... rest unchanged
}
```

## Recommendation

**Option 1 (Post-injection in runner.go)** is simpler:
- ✅ Only 1 file changed (runner.go)
- ✅ No function signature changes
- ✅ Clear injection point
- ✅ Easy to understand

**Option 2 (Inject in configloader)** is cleaner architecturally:
- ✅ Injection happens during config construction
- ✅ Config is complete when returned
- ❌ Requires changing function signatures
- ❌ Requires updating call sites

## My Recommendation: Option 1

Add the injection code in `runner.go` after line 210. This is the minimal, safest change.

```go
// After line 210 in cmd/epp/runner/runner.go
if eppConfig.FlowControlConfig != nil {
    workloadRegistry := ds.GetWorkloadRegistry()
    eppConfig.FlowControlConfig.Registry.WorkloadRegistry = workloadRegistry
    setupLog.Info("Injected WorkloadRegistry into FlowControl configuration")
}
```

This way:
1. Config is loaded normally
2. WorkloadRegistry is injected immediately after
3. When FlowRegistry is created (line 286), it already has the registry
4. No function signature changes needed
5. Minimal risk

**Awaiting your approval to proceed with Option 1.**