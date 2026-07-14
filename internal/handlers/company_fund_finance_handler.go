package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"monera-digital/internal/companyfund"
)

const (
	companyFundAdminKeyHeader   = "X-Company-Fund-Admin-Key"
	companyFundAdminActorHeader = "X-Company-Fund-Admin-Actor"
	maxCompanyFundAdminKeyBytes = 512
	maxCompanyFundRequestBytes  = 64 << 10
)

// CompanyFundFinanceHandlerConfig keeps the privileged management boundary
// separate from normal customer JWT authorization. The caller must omit route
// registration entirely when this configuration is unavailable.
type CompanyFundFinanceHandlerConfig struct {
	Store    companyfund.CompanyFundFinanceStore
	AdminKey string
}

// CompanyFundFinanceHandler exposes only financial reporting and the
// finance-owned classification mutation. It cannot access provider raw
// payloads and must only be mounted behind RequireAdminKey.
type CompanyFundFinanceHandler struct {
	store    companyfund.CompanyFundFinanceStore
	adminKey []byte
}

func NewCompanyFundFinanceHandler(config CompanyFundFinanceHandlerConfig) (*CompanyFundFinanceHandler, error) {
	if config.Store == nil {
		return nil, fmt.Errorf("company-fund finance store is required")
	}
	if config.AdminKey == "" || config.AdminKey != strings.TrimSpace(config.AdminKey) || len(config.AdminKey) > maxCompanyFundAdminKeyBytes {
		return nil, fmt.Errorf("company-fund admin key must be a non-blank bounded exact value")
	}
	return &CompanyFundFinanceHandler{
		store:    config.Store,
		adminKey: append([]byte(nil), config.AdminKey...),
	}, nil
}

// RequireAdminKey authenticates the management-only routes using a dedicated
// secret. It intentionally never reads Authorization or invokes the ordinary
// user JWT middleware. The comparison has fixed work for the configured key
// length, including when the supplied header is a different length.
func (h *CompanyFundFinanceHandler) RequireAdminKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h == nil || h.store == nil || len(h.adminKey) == 0 || !companyFundAdminKeyMatches(h.adminKey, c.GetHeader(companyFundAdminKeyHeader)) {
			companyFundFinanceError(c, http.StatusUnauthorized, "COMPANY_FUND_ADMIN_AUTH_REQUIRED", "company-fund admin authorization is required")
			return
		}
		c.Next()
	}
}

func companyFundAdminKeyMatches(expected []byte, provided string) bool {
	// Always compare slices of the same configured length. ConstantTimeCompare
	// would otherwise return immediately for a length mismatch.
	candidate := make([]byte, len(expected))
	copy(candidate, []byte(provided))
	sameLength := subtle.ConstantTimeEq(int32(len(provided)), int32(len(expected)))
	return subtle.ConstantTimeCompare(expected, candidate)&sameLength == 1
}

// GetDashboard returns exact-string currency aggregates and a canonical
// drilldown filter. It deliberately shares the store's default risk/dust
// inclusion contract with transaction detail requests.
func (h *CompanyFundFinanceHandler) GetDashboard(c *gin.Context) {
	filter, err := parseCompanyFundFinanceFilter(c)
	if err != nil {
		companyFundFinanceError(c, http.StatusBadRequest, "INVALID_COMPANY_FUND_FILTER", "invalid company-fund finance filter")
		return
	}
	filter, err = companyfund.CanonicalizeFinanceTransactionFilter(filter)
	if err != nil {
		companyFundFinanceError(c, http.StatusBadRequest, "INVALID_COMPANY_FUND_FILTER", "invalid company-fund finance filter")
		return
	}
	summary, err := h.store.GetFinanceDashboard(c.Request.Context(), filter)
	if err != nil {
		companyFundFinanceStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": summary})
}

// ListTransactions returns reportable transaction fields only. In particular,
// it never reads or serializes encrypted provider payload bytes.
func (h *CompanyFundFinanceHandler) ListTransactions(c *gin.Context) {
	filter, err := parseCompanyFundFinanceFilter(c)
	if err != nil {
		companyFundFinanceError(c, http.StatusBadRequest, "INVALID_COMPANY_FUND_FILTER", "invalid company-fund finance filter")
		return
	}
	filter, err = companyfund.CanonicalizeFinanceTransactionFilter(filter)
	if err != nil {
		companyFundFinanceError(c, http.StatusBadRequest, "INVALID_COMPANY_FUND_FILTER", "invalid company-fund finance filter")
		return
	}
	request, err := parseCompanyFundFinanceDetailRequest(c, filter)
	if err != nil {
		companyFundFinanceError(c, http.StatusBadRequest, "INVALID_COMPANY_FUND_PAGINATION", "invalid company-fund transaction pagination")
		return
	}
	request, err = companyfund.CanonicalizeFinanceTransactionDetailRequest(request)
	if err != nil {
		companyFundFinanceError(c, http.StatusBadRequest, "INVALID_COMPANY_FUND_PAGINATION", "invalid company-fund transaction pagination")
		return
	}
	details, err := h.store.ListFinanceTransactionDetails(c.Request.Context(), request)
	if err != nil {
		companyFundFinanceStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": details})
}

// UpdateClassification updates only finance-owned fields. The actor is a
// separate management header so the shared admin key is never stored as audit
// metadata and cannot be confused with a customer user identity.
func (h *CompanyFundFinanceHandler) UpdateClassification(c *gin.Context) {
	input, err := parseCompanyFundFinanceClassification(c)
	if err != nil {
		companyFundFinanceError(c, http.StatusBadRequest, "INVALID_COMPANY_FUND_CLASSIFICATION", "invalid company-fund classification update")
		return
	}
	input, err = companyfund.CanonicalizeFinanceClassificationUpdate(input)
	if err != nil {
		companyFundFinanceError(c, http.StatusBadRequest, "INVALID_COMPANY_FUND_CLASSIFICATION", "invalid company-fund classification update")
		return
	}
	result, err := h.store.UpdateFinanceTransactionClassification(c.Request.Context(), input)
	if err != nil {
		companyFundFinanceStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

func decodeCompanyFundJSON(c *gin.Context, target any) error {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return fmt.Errorf("company-fund request body is required")
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCompanyFundRequestBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("company-fund request must contain one JSON value")
		}
		return err
	}
	return nil
}

func companyFundFinanceStoreError(c *gin.Context, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		companyFundFinanceError(c, http.StatusServiceUnavailable, "COMPANY_FUND_UNAVAILABLE", "company-fund finance service is temporarily unavailable")
		return
	}
	// Store errors can contain database/constraint details. Do not return them
	// to a management browser, because report fields may identify counterparties
	// and the storage implementation must stay replaceable.
	companyFundFinanceError(c, http.StatusInternalServerError, "COMPANY_FUND_OPERATION_FAILED", "company-fund finance operation failed")
}

func companyFundFinanceError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"success": false,
		"error": gin.H{
			"code":    code,
			"message": message,
		},
	})
}
