package dashboard

import (
	"context"
	"time"
)

// Readiness is a read-only check of the running configuration. It never
// launches coding, changes access, or substitutes for a task's own validation.
type Readiness struct {
	State        string           `json:"state"`
	Label        string           `json:"label"`
	Detail       string           `json:"detail"`
	Message      string           `json:"message,omitempty"`
	CheckedAt    time.Time        `json:"checked_at"`
	LastContact  *time.Time       `json:"last_contact,omitempty"`
	Checks       []ReadinessCheck `json:"checks"`
	SetupCommand string           `json:"setup_command,omitempty"`
}

type ReadinessCheck struct {
	Name   string `json:"name"`
	State  string `json:"state"` // ready, attention, or unchecked
	Label  string `json:"label"`
	Detail string `json:"detail"`
}

type ReadinessService interface {
	Current() *Readiness
	Check(context.Context, bool) (*Readiness, error)
}
