package handlers

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"monera-digital/internal/companyfund"
)

type companyFundClassificationRequest struct {
	FinanceCategoryLevel1ID  *int64  `json:"financeCategoryLevel1Id"`
	FinanceCategoryLevel2ID  *int64  `json:"financeCategoryLevel2Id"`
	IsOperatingIncomeExpense *bool   `json:"isOperatingIncomeExpense"`
	Applicant                *string `json:"applicant"`
	BusinessDescription      *string `json:"businessDescription"`
	SummaryInclusionOverride *bool   `json:"summaryInclusionOverride"`
}

func parseCompanyFundFinanceFilter(c *gin.Context) (companyfund.FinanceTransactionFilter, error) {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return companyfund.FinanceTransactionFilter{}, fmt.Errorf("company-fund request is required")
	}
	values := c.Request.URL.Query()
	dateFrom, err := companyFundOptionalTimestamp(values, "dateFrom")
	if err != nil {
		return companyfund.FinanceTransactionFilter{}, err
	}
	dateTo, err := companyFundOptionalTimestamp(values, "dateTo")
	if err != nil {
		return companyfund.FinanceTransactionFilter{}, err
	}
	accountIDs, err := companyFundQueryInt64s(values, "accountId")
	if err != nil {
		return companyfund.FinanceTransactionFilter{}, err
	}
	level1IDs, err := companyFundQueryInt64s(values, "financeCategoryLevel1Id")
	if err != nil {
		return companyfund.FinanceTransactionFilter{}, err
	}
	level2IDs, err := companyFundQueryInt64s(values, "financeCategoryLevel2Id")
	if err != nil {
		return companyfund.FinanceTransactionFilter{}, err
	}
	operating, err := companyFundOptionalBool(values, "operatingIncomeExpense")
	if err != nil {
		return companyfund.FinanceTransactionFilter{}, err
	}
	includeSummaryExcluded, err := companyFundOptionalBool(values, "includeSummaryExcluded")
	if err != nil {
		return companyfund.FinanceTransactionFilter{}, err
	}

	filter := companyfund.FinanceTransactionFilter{
		DateFrom:                 dateFrom,
		DateTo:                   dateTo,
		AccountIDs:               accountIDs,
		FinanceCategoryLevel1IDs: level1IDs,
		FinanceCategoryLevel2IDs: level2IDs,
		OperatingIncomeExpense:   operating,
	}
	for _, value := range companyFundQueryValues(values, "channel") {
		filter.Channels = append(filter.Channels, companyfund.Channel(value))
	}
	for _, value := range companyFundQueryValues(values, "direction") {
		filter.Directions = append(filter.Directions, companyfund.Direction(value))
	}
	filter.Currencies = companyFundQueryValues(values, "currency")
	if includeSummaryExcluded != nil {
		filter.IncludeSummaryExcluded = *includeSummaryExcluded
	}
	return filter, nil
}

func parseCompanyFundFinanceDetailRequest(c *gin.Context, filter companyfund.FinanceTransactionFilter) (companyfund.FinanceTransactionDetailRequest, error) {
	values := c.Request.URL.Query()
	limit, err := companyFundOptionalInt(values, "limit")
	if err != nil {
		return companyfund.FinanceTransactionDetailRequest{}, err
	}
	offset, err := companyFundOptionalInt(values, "offset")
	if err != nil {
		return companyfund.FinanceTransactionDetailRequest{}, err
	}
	request := companyfund.FinanceTransactionDetailRequest{Filter: filter}
	if limit != nil {
		request.Limit = *limit
	}
	if offset != nil {
		request.Offset = *offset
	}
	return request, nil
}

func parseCompanyFundFinanceClassification(c *gin.Context) (companyfund.FinanceClassificationUpdate, error) {
	transactionID, err := strconv.ParseInt(strings.TrimSpace(c.Param("transactionID")), 10, 64)
	if err != nil || transactionID <= 0 {
		return companyfund.FinanceClassificationUpdate{}, fmt.Errorf("company-fund transaction ID must be positive")
	}
	actor := strings.TrimSpace(c.GetHeader(companyFundAdminActorHeader))
	if actor == "" {
		return companyfund.FinanceClassificationUpdate{}, fmt.Errorf("company-fund admin actor is required")
	}
	var request companyFundClassificationRequest
	if err := decodeCompanyFundJSON(c, &request); err != nil {
		return companyfund.FinanceClassificationUpdate{}, err
	}
	return companyfund.FinanceClassificationUpdate{
		TransactionID:            transactionID,
		FinanceCategoryLevel1ID:  request.FinanceCategoryLevel1ID,
		FinanceCategoryLevel2ID:  request.FinanceCategoryLevel2ID,
		IsOperatingIncomeExpense: request.IsOperatingIncomeExpense,
		Applicant:                request.Applicant,
		BusinessDescription:      request.BusinessDescription,
		SummaryInclusionOverride: request.SummaryInclusionOverride,
		UpdatedBy:                actor,
	}, nil
}

func companyFundOptionalTimestamp(values url.Values, key string) (*time.Time, error) {
	raw, present, err := companyFundSingleQueryValue(values, key)
	if err != nil || !present {
		return nil, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, fmt.Errorf("company-fund query %s must be RFC3339", key)
	}
	utc := parsed.UTC()
	return &utc, nil
}

func companyFundOptionalBool(values url.Values, key string) (*bool, error) {
	raw, present, err := companyFundSingleQueryValue(values, key)
	if err != nil || !present {
		return nil, err
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("company-fund query %s must be boolean", key)
	}
	return &parsed, nil
}

func companyFundOptionalInt(values url.Values, key string) (*int, error) {
	raw, present, err := companyFundSingleQueryValue(values, key)
	if err != nil || !present {
		return nil, err
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return nil, fmt.Errorf("company-fund query %s must be an integer", key)
	}
	return &parsed, nil
}

func companyFundSingleQueryValue(values url.Values, key string) (string, bool, error) {
	rawValues, found := values[key]
	if !found {
		return "", false, nil
	}
	if len(rawValues) != 1 || strings.TrimSpace(rawValues[0]) == "" {
		return "", false, fmt.Errorf("company-fund query %s must have one non-blank value", key)
	}
	return strings.TrimSpace(rawValues[0]), true, nil
}

func companyFundQueryValues(values url.Values, key string) []string {
	rawValues := values[key]
	items := make([]string, 0, len(rawValues))
	for _, raw := range rawValues {
		for _, item := range strings.Split(raw, ",") {
			items = append(items, strings.TrimSpace(item))
		}
	}
	return items
}

func companyFundQueryInt64s(values url.Values, key string) ([]int64, error) {
	items := companyFundQueryValues(values, key)
	result := make([]int64, 0, len(items))
	for _, item := range items {
		if item == "" {
			return nil, fmt.Errorf("company-fund query %s must not contain an empty ID", key)
		}
		parsed, err := strconv.ParseInt(item, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("company-fund query %s must contain integer IDs", key)
		}
		result = append(result, parsed)
	}
	return result, nil
}
