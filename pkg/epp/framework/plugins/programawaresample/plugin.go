package programawaresample

import (
	"time"
	"context"
	"encoding/json"
	"sync"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "sigs.k8s.io/gateway-api-inference-extension/pkg/common/observability/logging"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/datalayer"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/flowcontrol"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/plugin"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/requestcontrol"
	types "sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/scheduling"
)

const (
	ProgramAwarePluginType = "sample-program-aware-policy"

	// headers
	ProgramContextHeaderKey      = "x-program-context"
	ProgramIdResponseHeaderKey   = "x-program-id"

	// defaults
	DefaultCriticality = 3
)

type Plugin struct {
	typedName                plugin.TypedName
	programMetrics           sync.Map
	programContextPerRequest sync.Map
}

// Compile-time interface verification.
var _ requestcontrol.PreAdmissionPlugin = (*Plugin)(nil)
var _ flowcontrol.OrderingPolicy = (*Plugin)(nil)
var _ requestcontrol.PreRequest = (*Plugin)(nil)
var _ requestcontrol.ResponseReceived = (*Plugin)(nil)
var _ requestcontrol.ResponseComplete = (*Plugin)(nil)

func ProgramAwarePluginFactory(name string, rawParameters json.RawMessage, handle plugin.Handle) (plugin.Plugin, error) {
	p := &Plugin{
		typedName: plugin.TypedName{Type: ProgramAwarePluginType, Name: name},
	}

	log.FromContext(handle.Context()).V(logutil.DEFAULT).Info("Initialized Program aware sample plugin.")
	return p, nil
}

func (p *Plugin) TypedName() plugin.TypedName {
	return p.typedName
}

// pre admission plugin method
func (p *Plugin) PrepareAdmission(ctx context.Context, request *types.LLMRequest) error {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)
	preAdmissionTime := time.Now()

	// Extract program context from request header, or use defaults.
	programContext := p.extractProgramContext(request, logger)

	// Record pre-admission time and store context for this request.
	programContext.recordPreAdmissionTime(preAdmissionTime)
	p.programContextPerRequest.Store(request.RequestId, programContext)

	// Track request in program-level metrics.
	p.getOrCreateMetrics(programContext.ProgramID).RegisterNewRequest()

	logger.Info("PrepareAdmission complete",
		"requestId", request.RequestId,
		"programId", programContext.ProgramID,
		"criticality", programContext.Hints.Criticality,
	)
	return nil
}

func (p *Plugin) extractProgramContext(request *types.LLMRequest, logger logr.Logger) *ProgramContext {
	programContext := NewDefaultProgramContext()

	headerJSON, exists := request.Headers[ProgramContextHeaderKey]
	if !exists {
		logger.Info("Program context header not found, using defaults",
			"requestId", request.RequestId,
			"programId", programContext.ProgramID,
		)
		return programContext
	}

	if err := json.Unmarshal([]byte(headerJSON), programContext); err != nil {
		logger.Info("Failed to unmarshal program context, using defaults",
			"requestId", request.RequestId,
			"error", err,
		)
		return NewDefaultProgramContext()
	}

	return programContext
}

func (p *Plugin) getOrCreateMetrics(programID string) *ProgramMetrics {
	value, _ := p.programMetrics.LoadOrStore(programID, &ProgramMetrics{})
	return value.(*ProgramMetrics)
}

// flow control ordering policy plugin methods

func (p *Plugin) RequiredQueueCapabilities() []flowcontrol.QueueCapability {
	return []flowcontrol.QueueCapability{flowcontrol.CapabilityPriorityConfigurable}
}

func (p *Plugin) Less(a, b flowcontrol.QueueItemAccessor) bool {
	logger := log.Log.V(logutil.VERBOSE)

	aID := a.OriginalRequest().ID()
	bID := b.OriginalRequest().ID()
	aScore := p.score(aID)
	bScore := p.score(bID)
	result := aScore > bScore

	logger.Info("Less: comparing queue priority",
		"requestA", aID,
		"scoreA", aScore,
		"requestB", bID,
		"scoreB", bScore,
		"aBeforeB", result,
	)

	return result
}

func (p *Plugin) score(requestID string) float64 {
	ctxRaw, ok := p.programContextPerRequest.Load(requestID)
	if !ok {
		log.Log.V(logutil.VERBOSE).Info("score: no program context found, returning 0",
			"requestId", requestID,
		)
		return 0
	}
	programCtx := ctxRaw.(*ProgramContext)

	metrics := p.getOrCreateMetrics(programCtx.ProgramID)
	metrics.mu.RLock()
	defer metrics.mu.RUnlock()

	avgWaitMs := metrics.AverageWaitTime.Milliseconds()
	score := 0.4*float64(avgWaitMs) + 0.4*float64(programCtx.Hints.Criticality) - 0.2*float64(metrics.TotalRequests)

	log.Log.V(logutil.VERBOSE).Info("score: computed priority score",
		"requestId", requestID,
		"programId", programCtx.ProgramID,
		"criticality", programCtx.Hints.Criticality,
		"avgWaitTimeMs", avgWaitMs,
		"totalRequests", metrics.TotalRequests,
		"score", score,
	)

	return score
}

// pre request plugin method
func (p *Plugin) PreRequest(ctx context.Context, request *types.LLMRequest, schedulingResult *types.SchedulingResult) {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)

	ctxRaw, ok := p.programContextPerRequest.Load(request.RequestId)
	if !ok {
		logger.Info("PreRequest: no program context found, skipping",
			"requestId", request.RequestId,
		)
		return
	}
	programCtx := ctxRaw.(*ProgramContext)

	waitTime := programCtx.timeSincePreAdmission()
	p.getOrCreateMetrics(programCtx.ProgramID).RegisterRequestDispatched(waitTime)

	logger.Info("PreRequest: request dispatched to model server",
		"requestId", request.RequestId,
		"programId", programCtx.ProgramID,
		"criticality", programCtx.Hints.Criticality,
		"waitTimeMs", waitTime.Milliseconds(),
	)
}

// response received plugin method
func (p *Plugin) ResponseReceived(ctx context.Context, request *types.LLMRequest, response *requestcontrol.Response, targetEndpoint *datalayer.EndpointMetadata) {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)

	ctxRaw, ok := p.programContextPerRequest.Load(request.RequestId)
	if !ok {
		logger.Info("ResponseReceived: no program context found, skipping",
			"requestId", request.RequestId,
		)
		return
	}
	programCtx := ctxRaw.(*ProgramContext)

	if programCtx.ProgramID != "" {
		response.Headers[ProgramIdResponseHeaderKey] = programCtx.ProgramID
	}

	logger.Info("ResponseReceived: first response chunk received from model server",
		"requestId", request.RequestId,
		"programId", programCtx.ProgramID,
		"totalLatencyMs", programCtx.timeSincePreAdmission().Milliseconds(),
	)
}

// response complete plugin method
func (p *Plugin) ResponseComplete(ctx context.Context, request *types.LLMRequest, response *requestcontrol.Response, targetEndpoint *datalayer.EndpointMetadata) {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)

	ctxRaw, ok := p.programContextPerRequest.Load(request.RequestId)
	if !ok {
		logger.Info("ResponseComplete: no program context found, skipping",
			"requestId", request.RequestId,
		)
		return
	}
	programCtx := ctxRaw.(*ProgramContext)

	metrics := p.getOrCreateMetrics(programCtx.ProgramID)

	// Record token usage.
	metrics.RegisterRequestComplete(response.Usage.PromptTokens, response.Usage.CompletionTokens)

	logger.Info("ResponseComplete: request lifecycle complete",
		"requestId", request.RequestId,
		"programId", programCtx.ProgramID,
		"criticality", programCtx.Hints.Criticality,
		"promptTokens", response.Usage.PromptTokens,
		"completionTokens", response.Usage.CompletionTokens,
	)

	// Clean up per-request context.
	p.programContextPerRequest.Delete(request.RequestId)
}