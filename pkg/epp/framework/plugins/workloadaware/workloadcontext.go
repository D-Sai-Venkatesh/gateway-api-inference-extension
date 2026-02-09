package workloadaware

import (
    "time"

	"github.com/google/uuid"
)

type WorkloadContext struct {
	WorkloadID string         `json:"workload_id"`
	Hints      map[string]any `json:"hints"`
}

func NewDefaultWorkloadContext(config *Config) *WorkloadContext {
	workloadCtx := &WorkloadContext{}
	workloadCtx.ValidateAndApplyDefaults(config)
	return workloadCtx
}

func (wc *WorkloadContext) ValidateAndApplyDefaults(config *Config) {
	wc.validateAndApplyDefaultWorkloadID()
	wc.validateAndApplyDefaultHints(config)
}

func (wc *WorkloadContext) validateAndApplyDefaultWorkloadID() {
	if wc.WorkloadID == "" {
		wc.WorkloadID = "auto-" + uuid.NewString()
	}
}

func (wc *WorkloadContext) validateAndApplyDefaultHints(config *Config) {
	if wc.Hints == nil {
		wc.Hints = make(map[string]any)
	}

	wc.validateAndApplyDefaultCriticality(config)
    wc.validateEnqueueTime()
}

func (wc *WorkloadContext) validateAndApplyDefaultCriticality(config *Config) {
	rawCriticality, exists := wc.Hints[CriticalityWorkloadContextHintsKey]

	var finalCriticality int

	if !exists {
		finalCriticality = config.DefaultCriticality
	} else {
		switch v := rawCriticality.(type) {
		case int:
			finalCriticality = v
		case float64:
			finalCriticality = int(v)
		default:
			finalCriticality = config.DefaultCriticality
		}
	}

	if finalCriticality < config.MinCriticality {
		finalCriticality = config.MinCriticality
	} else if finalCriticality > config.MaxCriticality {
		finalCriticality = config.MaxCriticality
	}

	wc.Hints[CriticalityWorkloadContextHintsKey] = finalCriticality
}

func (wc *WorkloadContext) validateEnqueueTime() {
    enqueueTimeValue, exists := wc.Hints[EnqueueTimeWorkloadContextHintsKey]
    if exists {
        switch v := enqueueTimeValue.(type) {
        case string:
            enqueueTime, err := time.Parse(time.RFC3339, v)
            if err != nil {
                delete(wc.Hints, EnqueueTimeWorkloadContextHintsKey)
            } else {
                wc.Hints[EnqueueTimeWorkloadContextHintsKey] = enqueueTime
            }
        default:
            delete(wc.Hints, EnqueueTimeWorkloadContextHintsKey)
        }
    }
}
