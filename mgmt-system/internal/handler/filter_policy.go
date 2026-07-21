package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/ticket/email-filter-contract"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/service"
	"github.com/ticket/email-mgmt-system/internal/store"
	"gorm.io/gorm"
)

type FilterPolicyHandler struct {
	service      *service.FilterPolicyService
	sharedSecret string
}

func (h *FilterPolicyHandler) ConfigureQuarantineProxy(sharedSecret string) {
	h.sharedSecret = sharedSecret
}

func NewFilterPolicyHandler(policyService *service.FilterPolicyService) *FilterPolicyHandler {
	return &FilterPolicyHandler{service: policyService}
}

func (h *FilterPolicyHandler) RegisterAdminRoutes(r *gin.RouterGroup) {
	r.GET("/filter-policy-status", h.Status)

	r.GET("/manual-filter-revisions", h.ListManualRevisions)
	r.POST("/manual-filter-revisions", h.CreateManualRevision)
	r.GET("/manual-filter-revisions/:revision", h.GetManualRevision)
	r.PUT("/manual-filter-revisions/:revision", h.PutManualRevision)
	r.POST("/manual-filter-revisions/:revision/rules", h.AddManualRule)
	r.PUT("/manual-filter-revisions/:revision/rules/:logical_id", h.UpdateManualRule)
	r.DELETE("/manual-filter-revisions/:revision/rules/:logical_id", h.DeleteManualRule)
	r.POST("/manual-filter-revisions/:revision/validate", h.ValidateManualRevision)
	r.POST("/manual-filter-revisions/:revision/publish", h.PublishManualRevision)
	r.POST("/manual-filter-revisions/:revision/clone", h.CloneManualRevision)

	r.GET("/ad-filter-revisions", h.ListAdRevisions)
	r.POST("/ad-filter-revisions", h.CreateAdRevision)
	r.GET("/ad-filter-revisions/:revision", h.GetAdRevision)
	r.PUT("/ad-filter-revisions/:revision", h.PutAdRevision)
	r.POST("/ad-filter-revisions/:revision/detectors", h.AddAdDetector)
	r.PUT("/ad-filter-revisions/:revision/detectors/:logical_id", h.UpdateAdDetector)
	r.DELETE("/ad-filter-revisions/:revision/detectors/:logical_id", h.DeleteAdDetector)
	r.POST("/ad-filter-revisions/:revision/composites", h.AddAdComposite)
	r.PUT("/ad-filter-revisions/:revision/composites/:logical_id", h.UpdateAdComposite)
	r.DELETE("/ad-filter-revisions/:revision/composites/:logical_id", h.DeleteAdComposite)
	r.PUT("/ad-filter-revisions/:revision/weights/:symbol", h.PutAdWeight)
	r.DELETE("/ad-filter-revisions/:revision/weights/:symbol", h.DeleteAdWeight)
	r.POST("/ad-filter-revisions/:revision/validate", h.ValidateAdRevision)
	r.POST("/ad-filter-revisions/:revision/publish", h.PublishAdRevision)
	r.POST("/ad-filter-revisions/:revision/clone", h.CloneAdRevision)

	r.GET("/filter-decisions", h.ListDecisions)
	r.GET("/filter-decisions/:decision_key", h.GetDecision)
	r.GET("/filter-quarantines", h.ListQuarantines)
	r.GET("/filter-quarantines/:quarantine_key", h.GetQuarantine)
	r.GET("/filter-quarantines/:quarantine_key/message", h.GetQuarantineMessage)
	r.GET("/filter-quarantines/:quarantine_key/attachments/:index", h.GetQuarantineAttachment)
	r.POST("/filter-quarantines/:quarantine_key/release", h.ReleaseQuarantine)
	r.POST("/filter-quarantines/:quarantine_key/allow-and-release", h.AllowAndReleaseQuarantine)
	r.GET("/filter-quarantines/:quarantine_key/release-status", h.GetQuarantineReleaseStatus)
	r.POST("/filter-quarantines/:quarantine_key/confirm-ad", h.ConfirmQuarantineAd)
}

func (h *FilterPolicyHandler) ListQuarantines(c *gin.Context) {
	value, err := h.service.ListQuarantines(parsePositiveInt(c.Query("page"), 1), parsePositiveInt(c.Query("size"), 50), c.Query("status"))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "success", value)
}

func (h *FilterPolicyHandler) GetQuarantine(c *gin.Context) {
	value, err := h.service.GetQuarantine(c.Param("quarantine_key"))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "success", value)
}

func (h *FilterPolicyHandler) GetQuarantineMessage(c *gin.Context) {
	value, err := h.service.GetQuarantine(c.Param("quarantine_key"))
	if err != nil {
		h.writeError(c, err)
		return
	}
	data, err := proxyToServer(value.ServerAPIHost, http.MethodGet, "/internal/filter-quarantines/"+value.QuarantineKey+"/message", nil, h.sharedSecret)
	if err != nil {
		if !writeUpstreamJSONError(c, err) {
			serverError(c, ErrCodeExternalFail, "fetch quarantined message failed: "+err.Error())
		}
		return
	}
	writeJSON(c.Writer, data)
}

func (h *FilterPolicyHandler) GetQuarantineAttachment(c *gin.Context) {
	value, err := h.service.GetQuarantine(c.Param("quarantine_key"))
	if err != nil {
		h.writeError(c, err)
		return
	}
	path := "/internal/filter-quarantines/" + value.QuarantineKey + "/attachments/" + c.Param("index")
	proxyAttachmentToServer(c, value.ServerAPIHost, http.MethodGet, path, h.sharedSecret)
}

func (h *FilterPolicyHandler) ReleaseQuarantine(c *gin.Context) {
	var request struct {
		FeedbackLabel string `json:"feedback_label"`
	}
	if err := bindOptionalJSON(c, &request); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid release request: "+err.Error())
		return
	}
	if request.FeedbackLabel != "" && request.FeedbackLabel != "false_positive" && request.FeedbackLabel != "uncertain" {
		badRequest(c, ErrCodeParamInvalid, "feedback_label must be false_positive or uncertain")
		return
	}
	updated, err := h.executeQuarantineRelease(c.Param("quarantine_key"), policyRequestID(c), request.FeedbackLabel, policyActor(c))
	if err != nil {
		serverError(c, ErrCodeExternalFail, err.Error())
		return
	}
	if updated.Status == "release_failed" {
		fail(c, http.StatusBadGateway, ErrCodeExternalFail, updated.LastError)
		return
	}
	success(c, "quarantine released", updated)
}

func (h *FilterPolicyHandler) executeQuarantineRelease(key, operationID, feedbackLabel, actor string) (*service.FilterQuarantineView, error) {
	value, err := h.service.BeginQuarantineRelease(key, operationID, actor)
	if err != nil {
		return nil, err
	}
	operationID = value.ReleaseOperationID
	body, _ := json.Marshal(map[string]string{"operation_id": operationID})
	path := "/internal/filter-quarantines/" + value.QuarantineKey + "/release"
	data, callErr := proxyToServer(value.ServerAPIHost, http.MethodPost, path, bytes.NewReader(body), h.sharedSecret)
	receipt, receiptErr := receiptFromProxy(data, callErr)
	if receiptErr != nil {
		statusData, statusErr := proxyToServer(value.ServerAPIHost, http.MethodGet, "/internal/filter-quarantines/"+value.QuarantineKey+"/release-status", nil, h.sharedSecret)
		if statusErr == nil {
			receipt, receiptErr = receiptFromProxy(statusData, nil)
		}
	}
	if receiptErr != nil {
		return nil, fmt.Errorf("release response unavailable; retry resumes operation %s: %w", operationID, receiptErr)
	}
	if err := h.service.CompleteQuarantineRelease(value.QuarantineKey, operationID, feedbackLabel, actor, *receipt, callErr); err != nil {
		return nil, err
	}
	return h.service.GetQuarantine(value.QuarantineKey)
}

func (h *FilterPolicyHandler) AllowAndReleaseQuarantine(c *gin.Context) {
	var request struct {
		Scope string `json:"scope"`
	}
	if err := bindOptionalJSON(c, &request); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid allow-and-release request: "+err.Error())
		return
	}
	if request.Scope == "" {
		request.Scope = "email"
	}
	key := c.Param("quarantine_key")
	actor := policyActor(c)
	requestID := policyRequestID(c)
	value, err := h.service.GetQuarantine(key)
	if err != nil {
		h.writeError(c, err)
		return
	}
	data, err := proxyToServer(value.ServerAPIHost, http.MethodGet, "/internal/filter-quarantines/"+value.QuarantineKey+"/message", nil, h.sharedSecret)
	if err != nil {
		if !writeUpstreamJSONError(c, err) {
			serverError(c, ErrCodeExternalFail, "read quarantined sender failed: "+err.Error())
		}
		return
	}
	var envelope struct {
		Data struct {
			Message struct {
				From string `json:"from"`
			} `json:"message"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || strings.TrimSpace(envelope.Data.Message.From) == "" {
		badRequest(c, ErrCodeParamInvalid, "quarantined message has no valid sender")
		return
	}
	draft, err := h.service.CreateQuarantineAllowDraft(key, envelope.Data.Message.From, request.Scope, actor, requestID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	released, releaseErr := h.executeQuarantineRelease(key, requestID, "false_positive", actor)
	result := gin.H{"allow_draft": draft, "release": released}
	if releaseErr != nil {
		result["release_error"] = releaseErr.Error()
	} else if released.Status == "release_failed" {
		result["release_error"] = released.LastError
	}
	success(c, "allow draft created; release result recorded separately", result)
}

func (h *FilterPolicyHandler) GetQuarantineReleaseStatus(c *gin.Context) {
	value, err := h.service.GetQuarantine(c.Param("quarantine_key"))
	if err != nil {
		h.writeError(c, err)
		return
	}
	data, err := proxyToServer(value.ServerAPIHost, http.MethodGet, "/internal/filter-quarantines/"+value.QuarantineKey+"/release-status", nil, h.sharedSecret)
	if err != nil {
		if !writeUpstreamJSONError(c, err) {
			serverError(c, ErrCodeExternalFail, "release status failed: "+err.Error())
		}
		return
	}
	writeJSON(c.Writer, data)
}

func (h *FilterPolicyHandler) ConfirmQuarantineAd(c *gin.Context) {
	var request struct {
		Note string `json:"note"`
	}
	if err := bindOptionalJSON(c, &request); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid feedback request: "+err.Error())
		return
	}
	if err := h.service.ConfirmQuarantineAd(c.Param("quarantine_key"), request.Note, policyActor(c), policyRequestID(c)); err != nil {
		h.writeError(c, err)
		return
	}
	value, err := h.service.GetQuarantine(c.Param("quarantine_key"))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "advertising feedback recorded", value)
}

func receiptFromProxy(data []byte, callErr error) (*filtercontract.ReleaseReceipt, error) {
	if callErr != nil {
		var upstream *upstreamHTTPError
		if !errors.As(callErr, &upstream) {
			return nil, callErr
		}
		data = upstream.Body
	}
	var envelope struct {
		Data filtercontract.ReleaseReceipt `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	if err := envelope.Data.Validate(); err != nil {
		return nil, err
	}
	return &envelope.Data, nil
}

func (h *FilterPolicyHandler) ListDecisions(c *gin.Context) {
	page := parsePositiveInt(c.Query("page"), 1)
	size := parsePositiveInt(c.Query("size"), 50)
	value, err := h.service.ListDecisions(page, size, c.Query("action"))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "success", value)
}

func (h *FilterPolicyHandler) GetDecision(c *gin.Context) {
	value, err := h.service.GetDecision(c.Param("decision_key"))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "success", value)
}

func parsePositiveInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func (h *FilterPolicyHandler) RegisterInternalRoutes(r *gin.RouterGroup) {
	r.GET("/filter-bundles/:policy_kind", h.GetActiveBundle)
	r.POST("/filter-node-states", h.ReportNodeState)
	r.POST("/filter-decisions", h.RecordDecision)
}

func (h *FilterPolicyHandler) ListManualRevisions(c *gin.Context) {
	values, err := h.service.ListManualRevisions()
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "success", values)
}

func (h *FilterPolicyHandler) CreateManualRevision(c *gin.Context) {
	var request struct {
		BaseRevision *uint64 `json:"base_revision"`
	}
	if err := bindOptionalJSON(c, &request); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid draft request: "+err.Error())
		return
	}
	value, err := h.service.CreateManualDraft(request.BaseRevision, policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	created(c, "manual filter draft created", value)
}

func (h *FilterPolicyHandler) GetManualRevision(c *gin.Context) {
	value, err := h.service.GetManualRevision(parsePolicyRevision(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "success", value)
}

func (h *FilterPolicyHandler) PutManualRevision(c *gin.Context) {
	var request struct {
		Rules []filtercontract.ManualRule `json:"rules" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid manual policy: "+err.Error())
		return
	}
	value, err := h.service.PutManualRules(parsePolicyRevision(c), request.Rules, policyActor(c), policyRequestID(c), "replace_rules")
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "manual filter draft updated", value)
}

func (h *FilterPolicyHandler) AddManualRule(c *gin.Context) {
	var request filtercontract.ManualRule
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid manual rule: "+err.Error())
		return
	}
	value, err := h.service.AddManualRule(parsePolicyRevision(c), request, policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	created(c, "manual rule created", value)
}

func (h *FilterPolicyHandler) UpdateManualRule(c *gin.Context) {
	var request filtercontract.ManualRule
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid manual rule: "+err.Error())
		return
	}
	value, err := h.service.UpdateManualRule(parsePolicyRevision(c), c.Param("logical_id"), request, policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "manual rule updated", value)
}

func (h *FilterPolicyHandler) DeleteManualRule(c *gin.Context) {
	value, err := h.service.DeleteManualRule(parsePolicyRevision(c), c.Param("logical_id"), policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "manual rule deleted", value)
}

func (h *FilterPolicyHandler) ValidateManualRevision(c *gin.Context) {
	result, err := h.service.ValidateManualRevision(parsePolicyRevision(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "validation completed", result)
}

func (h *FilterPolicyHandler) PublishManualRevision(c *gin.Context) {
	value, err := h.service.PublishManualRevision(parsePolicyRevision(c), policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "manual filter revision published", value)
}

func (h *FilterPolicyHandler) CloneManualRevision(c *gin.Context) {
	revision := parsePolicyRevision(c)
	value, err := h.service.CreateManualDraft(&revision, policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	created(c, "manual filter revision cloned", value)
}

func (h *FilterPolicyHandler) ListAdRevisions(c *gin.Context) {
	values, err := h.service.ListAdRevisions()
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "success", values)
}

func (h *FilterPolicyHandler) CreateAdRevision(c *gin.Context) {
	var request struct {
		BaseRevision *uint64 `json:"base_revision"`
		Seed         string  `json:"seed"`
	}
	if err := bindOptionalJSON(c, &request); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid draft request: "+err.Error())
		return
	}
	value, err := h.service.CreateAdDraft(request.BaseRevision, request.Seed, policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	created(c, "ad filter draft created", value)
}

func (h *FilterPolicyHandler) GetAdRevision(c *gin.Context) {
	value, err := h.service.GetAdRevision(parsePolicyRevision(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "success", value)
}

func (h *FilterPolicyHandler) PutAdRevision(c *gin.Context) {
	var request struct {
		TagThreshold        *filtercontract.Score `json:"tag_threshold" binding:"required"`
		QuarantineThreshold *filtercontract.Score `json:"quarantine_threshold" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.TagThreshold == nil || request.QuarantineThreshold == nil {
		badRequest(c, ErrCodeParamInvalid, "tag_threshold and quarantine_threshold are required canonical decimals")
		return
	}
	value, err := h.service.PutAdThresholds(parsePolicyRevision(c), *request.TagThreshold, *request.QuarantineThreshold, policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "ad filter thresholds updated", value)
}

func (h *FilterPolicyHandler) AddAdDetector(c *gin.Context) {
	var request filtercontract.AdDetector
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid detector: "+err.Error())
		return
	}
	value, err := h.service.AddAdDetector(parsePolicyRevision(c), request, policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	created(c, "detector created", value)
}

func (h *FilterPolicyHandler) UpdateAdDetector(c *gin.Context) {
	var request filtercontract.AdDetector
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid detector: "+err.Error())
		return
	}
	value, err := h.service.UpdateAdDetector(parsePolicyRevision(c), c.Param("logical_id"), request, policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "detector updated", value)
}

func (h *FilterPolicyHandler) DeleteAdDetector(c *gin.Context) {
	value, err := h.service.DeleteAdDetector(parsePolicyRevision(c), c.Param("logical_id"), policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "detector deleted", value)
}

func (h *FilterPolicyHandler) AddAdComposite(c *gin.Context) {
	var request filtercontract.AdComposite
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid composite: "+err.Error())
		return
	}
	value, err := h.service.AddAdComposite(parsePolicyRevision(c), request, policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	created(c, "composite created", value)
}

func (h *FilterPolicyHandler) UpdateAdComposite(c *gin.Context) {
	var request filtercontract.AdComposite
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid composite: "+err.Error())
		return
	}
	value, err := h.service.UpdateAdComposite(parsePolicyRevision(c), c.Param("logical_id"), request, policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "composite updated", value)
}

func (h *FilterPolicyHandler) DeleteAdComposite(c *gin.Context) {
	value, err := h.service.DeleteAdComposite(parsePolicyRevision(c), c.Param("logical_id"), policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "composite deleted", value)
}

func (h *FilterPolicyHandler) PutAdWeight(c *gin.Context) {
	var request struct {
		Score *filtercontract.Score `json:"score" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.Score == nil {
		badRequest(c, ErrCodeParamInvalid, "score is required and must use at most three decimal places")
		return
	}
	value, err := h.service.PutAdWeight(parsePolicyRevision(c), c.Param("symbol"), *request.Score, policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "symbol weight saved", value)
}

func (h *FilterPolicyHandler) DeleteAdWeight(c *gin.Context) {
	value, err := h.service.DeleteAdWeight(parsePolicyRevision(c), c.Param("symbol"), policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "symbol weight deleted", value)
}

func (h *FilterPolicyHandler) ValidateAdRevision(c *gin.Context) {
	result, err := h.service.ValidateAdRevision(parsePolicyRevision(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "validation completed", result)
}

func (h *FilterPolicyHandler) PublishAdRevision(c *gin.Context) {
	value, err := h.service.PublishAdRevision(parsePolicyRevision(c), policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "ad filter revision published", value)
}

func (h *FilterPolicyHandler) CloneAdRevision(c *gin.Context) {
	revision := parsePolicyRevision(c)
	value, err := h.service.CreateAdDraft(&revision, "", policyActor(c), policyRequestID(c))
	if err != nil {
		h.writeError(c, err)
		return
	}
	created(c, "ad filter revision cloned", value)
}

func (h *FilterPolicyHandler) Status(c *gin.Context) {
	value, err := h.service.Status()
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "success", value)
}

func (h *FilterPolicyHandler) GetActiveBundle(c *gin.Context) {
	value, err := h.service.ActiveBundle(c.Param("policy_kind"))
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "success", value)
}

func (h *FilterPolicyHandler) ReportNodeState(c *gin.Context) {
	var state model.FilterNodeState
	if err := c.ShouldBindJSON(&state); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid node filter state: "+err.Error())
		return
	}
	if err := h.service.ReportNodeState(&state); err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "filter node state recorded", state)
}

func (h *FilterPolicyHandler) RecordDecision(c *gin.Context) {
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, 5<<20))
	if err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid filter decision event: "+err.Error())
		return
	}
	var event filtercontract.OutboxEvent
	if err := filtercontract.DecodeStrict(data, &event); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid filter decision event: "+err.Error())
		return
	}
	decision, err := h.service.RecordDecision(event)
	if err != nil {
		h.writeError(c, err)
		return
	}
	success(c, "filter decision recorded", decision)
}

func (h *FilterPolicyHandler) writeError(c *gin.Context, err error) {
	var contractError *filtercontract.ContractError
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		notFound(c, "filter policy revision or resource not found")
	case errors.Is(err, store.ErrFilterPolicyImmutable), errors.Is(err, service.ErrFilterPolicyDraftRequired), errors.Is(err, store.ErrFilterPolicyConflict):
		fail(c, http.StatusConflict, ErrCodeBusiness, err.Error())
	case errors.Is(err, store.ErrFilterDecisionConflict):
		fail(c, http.StatusConflict, ErrCodeBusiness, err.Error())
	case errors.Is(err, store.ErrFilterQuarantineConflict):
		fail(c, http.StatusConflict, ErrCodeBusiness, err.Error())
	case errors.As(err, &contractError), errors.Is(err, service.ErrFilterPolicySeed),
		errors.Is(err, service.ErrFilterDecisionNode), errors.Is(err, store.ErrInvalidFilterDecision),
		errors.Is(err, store.ErrInvalidFilterPolicyRevision), errors.Is(err, store.ErrInvalidFilterNodeState),
		errors.Is(err, store.ErrInvalidFilterQuarantine):
		badRequest(c, ErrCodeParamInvalid, err.Error())
	default:
		serverError(c, ErrCodeInternal, "filter policy operation failed: "+err.Error())
	}
}

func parsePolicyRevision(c *gin.Context) uint64 {
	return parseUint64(c.Param("revision"))
}

func policyActor(c *gin.Context) string {
	if actor, ok := c.Get("admin_user"); ok {
		if value, ok := actor.(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "internal"
}

func policyRequestID(c *gin.Context) string {
	for _, header := range []string{"Idempotency-Key", "X-Request-ID"} {
		if value := strings.TrimSpace(c.GetHeader(header)); value != "" {
			if len(value) > 64 {
				return value[:64]
			}
			return value
		}
	}
	return uuid.NewString()
}

func bindOptionalJSON(c *gin.Context, target any) error {
	if c.Request.Body == nil || c.Request.ContentLength == 0 {
		return nil
	}
	return c.ShouldBindJSON(target)
}
