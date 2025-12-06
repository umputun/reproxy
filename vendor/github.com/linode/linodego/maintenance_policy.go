package linodego

import (
	"context"
)

type MaintenancePolicy struct {
	Slug                  string `json:"slug"`
	Label                 string `json:"label"`
	Description           string `json:"description"`
	Type                  string `json:"type"`
	NotificationPeriodSec int    `json:"notification_period_sec"`
	IsDefault             bool   `json:"is_default"`
}

// ListMaintenancePolicies can only be used with v4beta.
func (c *Client) ListMaintenancePolicies(ctx context.Context, opts *ListOptions) ([]MaintenancePolicy, error) {
	return getPaginatedResults[MaintenancePolicy](ctx, c, "maintenance/policies", opts)
}
