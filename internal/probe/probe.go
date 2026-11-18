package probe

import (
	"context"

	"github.com/Queens-Rocket-Engineering-Team/ctl-havoc/internal/timeline"
)

// shared interface for probes
type Probe interface {
	Name() string
	Start(ctx context.Context, t timeline.Timeline) error
}
