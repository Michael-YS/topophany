package api

import "context"

// ServiceEdit changes only service metadata, retaining private topology fields.
type ServiceEdit struct {
	Current     func() bool `json:"-"`
	ID          string      `json:"id"`
	Revision    string      `json:"revision"`
	Delete      bool        `json:"delete"`
	Name        string      `json:"name"`
	Environment string      `json:"environment"`
}

type ServiceEditor func(context.Context, ServiceEdit) error
