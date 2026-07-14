package companyfund

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The public Financial Transactions reference requires ISO-8601 timestamps
// and makes both range endpoints inclusive, but does not publish a
// second/millisecond-only restriction. Keep the full RFC3339Nano precision we
// send on the wire so two adjacent internal [from,to) intervals do not drop
// the final fractional second of the first interval.
const airwallexFinancialTransactionsQueryTimePrecision = time.Nanosecond

// ListFinancialTransactions retrieves a provider page without normalizing it.
// The caller receives exact provider facts and can later map them into the
// company-fund model without losing the original raw payload. No status filter
// is sent: the documented filter cannot request CANCELLED records even though
// they can appear in the response lifecycle, and reconciliation must retain
// those corrections.
func (c *AirwallexClient) ListFinancialTransactions(ctx context.Context, input AirwallexFinancialTransactionsRequest) (AirwallexFinancialTransactionsPage, error) {
	pageSize, err := validateAirwallexFinancialTransactionsRequest(input)
	if err != nil {
		return AirwallexFinancialTransactionsPage{}, err
	}
	fromCreatedAt, toCreatedAt, err := airwallexFinancialTransactionsInclusiveQueryWindow(input)
	if err != nil {
		return AirwallexFinancialTransactionsPage{}, err
	}
	query := url.Values{
		"from_created_at": []string{fromCreatedAt.Format(time.RFC3339Nano)},
		"to_created_at":   []string{toCreatedAt.Format(time.RFC3339Nano)},
		"page_num":        []string{strconv.Itoa(input.PageNum)},
		"page_size":       []string{strconv.Itoa(pageSize)},
	}
	body, err := c.authenticatedGET(ctx, c.endpoint("/api/v1/financial_transactions", query))
	if err != nil {
		return AirwallexFinancialTransactionsPage{}, err
	}
	return decodeAirwallexFinancialTransactionsPage(body)
}

// GetFinancialTransaction retrieves one known Financial Transactions record by
// its provider ID. It is intentionally a narrow reconciliation primitive:
// webhook events are delivery notifications, while list/detail responses are
// the provider's balance-history facts.
func (c *AirwallexClient) GetFinancialTransaction(ctx context.Context, providerID string) (AirwallexFinancialTransaction, error) {
	providerID, err := validateAirwallexFinancialTransactionID(providerID)
	if err != nil {
		return AirwallexFinancialTransaction{}, err
	}
	body, err := c.authenticatedGET(ctx, c.endpoint("/api/v1/financial_transactions/"+providerID, nil))
	if err != nil {
		return AirwallexFinancialTransaction{}, err
	}
	return decodeAirwallexFinancialTransaction(body)
}

func validateAirwallexFinancialTransactionsRequest(input AirwallexFinancialTransactionsRequest) (int, error) {
	if input.FromCreatedAt.IsZero() || input.ToCreatedAt.IsZero() || !input.FromCreatedAt.Before(input.ToCreatedAt) {
		return 0, fmt.Errorf("airwallex financial transactions require a non-empty time window")
	}
	if input.PageNum < 0 || input.PageNum > maxAirwallexFinancialTransactionPageNumber {
		return 0, fmt.Errorf("airwallex financial transactions page number is outside configured bounds")
	}
	pageSize := input.PageSize
	if pageSize == 0 {
		pageSize = defaultAirwallexFinancialTransactionPageSize
	}
	if pageSize < 1 || pageSize > maxAirwallexFinancialTransactionPageSize {
		return 0, fmt.Errorf("airwallex financial transactions page size is outside configured bounds")
	}
	return pageSize, nil
}

// airwallexFinancialTransactionsInclusiveQueryWindow maps our internal UTC
// [from,to) convention to Airwallex's documented inclusive
// from_created_at/to_created_at filters. The provider documentation does not
// constrain ISO-8601 values to whole seconds, so the adapter preserves the
// RFC3339Nano precision it sends and subtracts one nanosecond from the
// exclusive end. This prevents overlap without silently losing a fractional
// tail while leaving a future provider-documented coarser precision as an
// explicit contract change rather than an implicit truncation.
func airwallexFinancialTransactionsInclusiveQueryWindow(input AirwallexFinancialTransactionsRequest) (time.Time, time.Time, error) {
	fromCreatedAt := input.FromCreatedAt.UTC()
	toCreatedAt := input.ToCreatedAt.UTC()
	if toCreatedAt.Sub(fromCreatedAt) < airwallexFinancialTransactionsQueryTimePrecision {
		return time.Time{}, time.Time{}, fmt.Errorf("airwallex financial transactions window is smaller than the provider query precision")
	}
	return fromCreatedAt, toCreatedAt.Add(-airwallexFinancialTransactionsQueryTimePrecision), nil
}

func validateAirwallexFinancialTransactionID(value string) (string, error) {
	providerID := strings.TrimSpace(value)
	if providerID == "" || len(providerID) > maxAirwallexFinancialTransactionIDBytes || strings.ContainsAny(providerID, "/?#%\\") {
		return "", fmt.Errorf("airwallex financial transaction ID is invalid")
	}
	return providerID, nil
}

func decodeAirwallexFinancialTransactionsPage(body []byte) (AirwallexFinancialTransactionsPage, error) {
	var response struct {
		Items   []json.RawMessage `json:"items"`
		HasMore bool              `json:"has_more"`
	}
	if err := decodeAirwallexJSON(body, &response); err != nil {
		return AirwallexFinancialTransactionsPage{}, err
	}
	items := make([]AirwallexFinancialTransaction, 0, len(response.Items))
	for _, raw := range response.Items {
		item, err := decodeAirwallexFinancialTransaction(raw)
		if err != nil {
			return AirwallexFinancialTransactionsPage{}, err
		}
		items = append(items, item)
	}
	return AirwallexFinancialTransactionsPage{
		Items:   items,
		HasMore: response.HasMore,
	}, nil
}

func decodeAirwallexFinancialTransaction(raw json.RawMessage) (AirwallexFinancialTransaction, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return AirwallexFinancialTransaction{}, ErrAirwallexMalformedResponse
	}
	var decoded struct {
		ProviderID         string          `json:"id"`
		Amount             json.RawMessage `json:"amount"`
		BatchID            string          `json:"batch_id"`
		ClientRate         json.RawMessage `json:"client_rate"`
		CreatedAt          string          `json:"created_at"`
		Currency           string          `json:"currency"`
		CurrencyPair       string          `json:"currency_pair"`
		Description        string          `json:"description"`
		EstimatedSettledAt string          `json:"estimated_settled_at"`
		Fee                json.RawMessage `json:"fee"`
		FundingSourceID    string          `json:"funding_source_id"`
		Net                json.RawMessage `json:"net"`
		SettledAt          string          `json:"settled_at"`
		SourceID           string          `json:"source_id"`
		SourceType         string          `json:"source_type"`
		Status             string          `json:"status"`
		TransactionType    string          `json:"transaction_type"`
	}
	if err := decodeAirwallexJSON(trimmed, &decoded); err != nil {
		return AirwallexFinancialTransaction{}, err
	}
	return AirwallexFinancialTransaction{
		ProviderID:         decoded.ProviderID,
		Amount:             append(json.RawMessage(nil), decoded.Amount...),
		BatchID:            decoded.BatchID,
		ClientRate:         append(json.RawMessage(nil), decoded.ClientRate...),
		CreatedAt:          decoded.CreatedAt,
		Currency:           decoded.Currency,
		CurrencyPair:       decoded.CurrencyPair,
		Description:        decoded.Description,
		EstimatedSettledAt: decoded.EstimatedSettledAt,
		Fee:                append(json.RawMessage(nil), decoded.Fee...),
		FundingSourceID:    decoded.FundingSourceID,
		Net:                append(json.RawMessage(nil), decoded.Net...),
		SettledAt:          decoded.SettledAt,
		SourceID:           decoded.SourceID,
		SourceType:         decoded.SourceType,
		Status:             decoded.Status,
		TransactionType:    decoded.TransactionType,
		Raw:                append(json.RawMessage(nil), trimmed...),
	}, nil
}
