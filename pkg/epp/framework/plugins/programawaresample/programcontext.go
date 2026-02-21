package programawaresample

import (
	"time"

	"github.com/google/uuid"
)

const (
	PreAdmissionTimeMetadataKey = "preAdmissionTime"
)

type Hints struct {
	Criticality int `json:"criticality"`
}

type ProgramContext struct {
	ProgramID string            `json:"program_id"`
	Hints     Hints             `json:"hints"`
	Metadata  map[string]any    `json:"metadata"`
}

func NewDefaultProgramContext() *ProgramContext {
	return &ProgramContext{
		ProgramID: uuid.NewString(),
		Hints: Hints{
			Criticality: DefaultCriticality,
		},
		Metadata: make(map[string]any),
	}
}

func (p *ProgramContext) recordPreAdmissionTime(t time.Time) {
	p.Metadata[PreAdmissionTimeMetadataKey] = t
}

func (p *ProgramContext) timeSincePreAdmission() time.Duration {
	t, ok := p.Metadata[PreAdmissionTimeMetadataKey].(time.Time)
	if !ok {
		return 0
	}
	return time.Since(t)
}
