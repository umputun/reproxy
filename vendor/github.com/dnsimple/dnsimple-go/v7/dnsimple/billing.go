package dnsimple

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
)

type ListChargesOptions struct {
	// Only include results after the given date.
	StartDate string `url:"start_date,omitempty"`

	// Only include results before the given date.
	EndDate string `url:"end_date,omitempty"`

	// Sort results. Default sorting is by invoiced ascending.
	Sort string `url:"sort,omitempty"`
}

type ListChargesResponse struct {
	Response
	Data []Charge `json:"data"`
}

type Charge struct {
	InvoicedAt    string          `json:"invoiced_at,omitempty"`
	TotalAmount   decimal.Decimal `json:"total_amount,omitempty"`
	BalanceAmount decimal.Decimal `json:"balance_amount,omitempty"`
	Reference     string          `json:"reference,omitempty"`
	State         string          `json:"state,omitempty"`
	Items         []ChargeItem    `json:"items,omitempty"`
}

type ChargeItem struct {
	Description      string          `json:"description,omitempty"`
	Amount           decimal.Decimal `json:"amount,omitempty"`
	ProductId        int64           `json:"product_id,omitempty"`
	ProductType      string          `json:"product_type,omitempty"`
	ProductReference string          `json:"product_reference,omitempty"`
}

type BillingService struct {
	client *Client
}

// Lists the billing charges for the account.
//
// See https://developer.dnsimple.com/v2/billing/#listCharges
func (s *BillingService) ListCharges(ctx context.Context, account string, options ListChargesOptions) (*ListChargesResponse, error) {
	path := fmt.Sprintf("/v2/%v/billing/charges", account)
	listResponse := &ListChargesResponse{}

	path, err := addURLQueryOptions(path, options)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.get(ctx, path, listResponse)
	if err != nil {
		return nil, err
	}

	listResponse.HTTPResponse = resp
	return listResponse, nil
}
