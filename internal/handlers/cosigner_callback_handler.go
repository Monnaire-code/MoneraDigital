package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	sdkCosigner "github.com/Safeheron/safeheron-api-sdk-go/safeheron/cosigner"
	"github.com/gin-gonic/gin"

	"monera-digital/internal/approval"
	"monera-digital/internal/safeheron"
)

type CosignerParser interface {
	ParseRequest(sdkCosigner.CoSignerCallBackV3) (*safeheron.CoSignerBizContentV3, error)
	BuildResponse(approvalId, action string) (map[string]string, error)
}

type CosignerEvaluator interface {
	Evaluate(ctx context.Context, biz *safeheron.CoSignerBizContentV3) (*approval.ApprovalDecision, error)
}

type CosignerCallbackHandler struct {
	Parser     CosignerParser
	Evaluator  CosignerEvaluator
	AllowedIPs []string
	AlertFn    approval.AlertFunc
}

func NewCosignerCallbackHandler(client CosignerParser, svc CosignerEvaluator, allowedIPs []string, alertFn approval.AlertFunc) *CosignerCallbackHandler {
	return &CosignerCallbackHandler{
		Parser:     client,
		Evaluator:  svc,
		AllowedIPs: allowedIPs,
		AlertFn:    alertFn,
	}
}

func (h *CosignerCallbackHandler) Handle(c *gin.Context) {
	if h == nil || h.Parser == nil || h.Evaluator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "COSIGNER_UNAVAILABLE",
			"message": "Cosigner callback handler not initialised",
		})
		return
	}

	if len(h.AllowedIPs) > 0 {
		clientIP := c.ClientIP()
		allowed := false
		for _, ip := range h.AllowedIPs {
			if ip == clientIP {
				allowed = true
				break
			}
		}
		if !allowed {
			log.Printf("[cosigner] REJECT ip=%s not in allowlist", clientIP)
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxWebhookBodyBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			c.AbortWithStatus(http.StatusRequestEntityTooLarge)
			return
		}
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	var callbackReq sdkCosigner.CoSignerCallBackV3
	if err := json.Unmarshal(body, &callbackReq); err != nil {
		log.Printf("[cosigner] REJECT: invalid JSON: %v", err)
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	biz, err := h.Parser.ParseRequest(callbackReq)
	if err != nil {
		log.Printf("[cosigner] REJECT: verify failed: %v", err)
		if h.AlertFn != nil {
			h.AlertFn("ERROR", "审批回调验签失败", map[string]string{
				"ip":    c.ClientIP(),
				"error": err.Error(),
			})
		}
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	decision, err := h.Evaluator.Evaluate(ctx, biz)
	if err != nil {
		log.Printf("[cosigner] ERROR: evaluate failed approvalId=%s: %v", biz.ApprovalId, err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// BuildResponse 是纯 CPU 的 RSA-PSS 签名，无需 ctx 超时保护
	resp, err := h.Parser.BuildResponse(biz.ApprovalId, decision.Action)
	if err != nil {
		log.Printf("[cosigner] ERROR: build response failed approvalId=%s: %v", biz.ApprovalId, err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	log.Printf("[cosigner] OK approvalId=%s type=%s action=%s", biz.ApprovalId, biz.Type, decision.Action)
	c.JSON(http.StatusOK, resp)
}
