package workloadaware

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
	// "math"

	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/flowcontrol"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/plugin"
	types "sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/scheduling"
)

const (
	WorkloadAwarePluginType  = "workload-aware-policy"
	WorkloadContextHeaderKey = "x-workload-context"

	CriticalityWorkloadContextHintsKey = "criticality"
	EnqueueTimeWorkloadContextHintsKey = "enqueue-time"
)

type Config struct {
	// workload context config
	WorkloadContextHeaderKey string
	DefaultCriticality       int
	MinCriticality           int
	MaxCriticality           int

	// workload registry config
	EMAAlpha          float64
	RequestRateWindow int // in seconds
}

var DefaultConfig = Config{
	WorkloadContextHeaderKey: WorkloadContextHeaderKey,
	DefaultCriticality:       3,
	MinCriticality:           1,
	MaxCriticality:           5,
	EMAAlpha:                 0.2,
	RequestRateWindow:        60,
}

type Plugin struct {
	typedName                 plugin.TypedName
	config                    *Config
	workloadRegistry          *WorkloadRegistry
	workloadContextMap        sync.Map
}

func WorkloadAwarePluginFactory(name string, rawParameters json.RawMessage, handle plugin.Handle) (plugin.Plugin, error) {
	parameters := DefaultConfig

	if rawParameters != nil {
		if err := json.Unmarshal(rawParameters, &parameters); err != nil {
			return nil, fmt.Errorf("failed to parse the parameters of the %s plugin. Error: %s", WorkloadAwarePluginType, err)
		}
	}

	wr := NewWorkloadRegistry(time.Duration(parameters.RequestRateWindow)*time.Second, parameters.EMAAlpha)

	p := &Plugin{
		typedName: plugin.TypedName{Type: WorkloadAwarePluginType, Name: name},
		config:    &parameters,
		workloadRegistry:        wr,
	}

	return p, nil
}

func (p *Plugin) TypedName() plugin.TypedName {
	return p.typedName
}

func (p *Plugin) Produces() map[string]any {
	return map[string]any{}
}

func (p *Plugin) Consumes() map[string]any {
	return map[string]any{}
}

// prepare request data plugin
func (p *Plugin) PrepareRequestData(ctx context.Context, request *types.LLMRequest, pods []types.Endpoint) error {
	log.Printf("Prepare Request Data: call for requestID:%s", request.RequestId)
	workloadCtx := extractWorkloadContext(request, p.config)
	workloadCtx.Hints[EnqueueTimeWorkloadContextHintsKey] = time.Now()
	
	p.workloadContextMap.Store(request.RequestId, workloadCtx)

	log.Printf("Prepare Request Data: requestID:%s", request.RequestId)
	p.workloadRegistry.WorkloadHandleNewRequest(workloadCtx.WorkloadID)
	return nil
}

func extractWorkloadContext(request *types.LLMRequest, config *Config) *WorkloadContext {
	workloadContextJSON, exists := request.Headers[config.WorkloadContextHeaderKey]
	if !exists || workloadContextJSON == "" {
		return NewDefaultWorkloadContext(config)
	}

	var workloadCtx WorkloadContext
	if err := json.Unmarshal([]byte(workloadContextJSON), &workloadCtx); err != nil {
		return NewDefaultWorkloadContext(config)
	}

	workloadCtx.ValidateAndApplyDefaults(config)
	return &workloadCtx
}

// pre request data plugin
func (p *Plugin) PreRequest(ctx context.Context, request *types.LLMRequest, schedulingResult *types.SchedulingResult) {
	log.Printf("Pre Request Data: call for requestID:%s", request.RequestId)
	workloadCtxRaw, loaded := p.workloadContextMap.LoadOrStore(request.RequestId, NewDefaultWorkloadContext(p.config))

	workloadCtx := workloadCtxRaw.(*WorkloadContext)

	enqueueTime, exists := workloadCtx.Hints[EnqueueTimeWorkloadContextHintsKey]
	if !exists {
		return
	}

	log.Printf("Pre Request: requestID:%s got enqueue time: %s, loaded %t", request.RequestId, enqueueTime.(time.Time).String(), loaded)
	p.workloadRegistry.WorkloadHandleDispatchedRequest(workloadCtx.WorkloadID, time.Since(enqueueTime.(time.Time)))
}

// flow control ordering policy plugin
func (p *Plugin) RequiredQueueCapabilities() []flowcontrol.QueueCapability {
	return []flowcontrol.QueueCapability{flowcontrol.CapabilityPriorityConfigurable}
}

func (p *Plugin) Less(a, b flowcontrol.QueueItemAccessor) bool {
	log.Printf("Less is called for request ID a: %s, request ID b: %s", a.OriginalRequest().ID(), b.OriginalRequest().ID())
	scoreA := p.score(a.OriginalRequest().ID())
	scoreB := p.score(b.OriginalRequest().ID())
	log.Printf("Score for request ID a: %s, request ID b: %s, scoreA: %f, scoreB: %f", a.OriginalRequest().ID(), b.OriginalRequest().ID(), scoreA, scoreB)
	return scoreA < scoreB
}

func (p *Plugin) score(requestId string) float64 {
	log.Printf("Score is called for request ID: %s", requestId)
	workloadCtxRaw, loaded := p.workloadContextMap.LoadOrStore(requestId, NewDefaultWorkloadContext(p.config))
	workloadCtx := workloadCtxRaw.(*WorkloadContext)

	metrics := p.workloadRegistry.GetMetrics(workloadCtx.WorkloadID)
	if metrics == nil {
		return 0
	}

	score := 0.4 * float64(metrics.AverageWaitTime) + 0.4 * float64(workloadCtx.Hints[CriticalityWorkloadContextHintsKey].(int)) - 0.2 * float64(metrics.SlidingWindowRequests)
	log.Printf("Score loaded %t", loaded)
	return score
}

