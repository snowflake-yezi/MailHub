package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/service"
	nodecontract "github.com/ticket/email-node-contract"
)

type NodeEnrollmentHandler struct {
	service        *service.NodeEnrollmentService
	sessionRevoker interface {
		DisconnectServer(uint64, error) bool
	}
}

type credentialSessionRevoker interface {
	DisconnectCredential(serverID, credentialID uint64, cause error) bool
}

type credentialSessionExpirer interface {
	ExpireCredentialAt(serverID, credentialID uint64, expiresAt time.Time) bool
}

func NewNodeEnrollmentHandler(enrollmentService *service.NodeEnrollmentService) *NodeEnrollmentHandler {
	return &NodeEnrollmentHandler{service: enrollmentService}
}

func (handler *NodeEnrollmentHandler) ConfigureSessionRevoker(revoker interface {
	DisconnectServer(uint64, error) bool
}) {
	handler.sessionRevoker = revoker
}

func (handler *NodeEnrollmentHandler) RegisterAdminRoutes(group *gin.RouterGroup) {
	group.POST(adminRoute(nodecontract.AdminNodeEnrollmentsRoute), handler.CreateEnrollment)
	group.GET(adminRoute(nodecontract.AdminNodeEnrollmentsRoute), handler.ListEnrollments)
	group.POST(adminRoute(nodecontract.AdminNodeEnrollmentRevokeRoute), handler.RevokeEnrollment)
	group.DELETE(adminRoute(nodecontract.AdminNodeEnrollmentDeleteRoute), handler.DeleteEnrollment)
	group.GET(adminRoute(nodecontract.AdminNodeEnrollmentRequestsRoute), handler.ListRequests)
	group.GET(adminRoute(nodecontract.AdminNodeEnrollmentRequestRoute), handler.GetRequest)
	group.POST(adminRoute(nodecontract.AdminNodeEnrollmentRequestApproveRoute), handler.ApproveRequest)
	group.POST(adminRoute(nodecontract.AdminNodeEnrollmentRequestRejectRoute), handler.RejectRequest)
	group.GET(adminRoute(nodecontract.AdminNodeCredentialsRoute), handler.ListCredentials)
	group.POST(adminRoute(nodecontract.AdminNodeCredentialRotateRoute), handler.RotateCredential)
	group.POST(adminRoute(nodecontract.AdminNodeCredentialRevokeRoute), handler.RevokeCredentials)
	group.POST(adminRoute(nodecontract.AdminNodeCredentialRevokeOneRoute), handler.RevokeCredential)
	group.DELETE(adminRoute(nodecontract.AdminNodeCredentialDeleteRoute), handler.DeleteCredential)
	group.POST(adminRoute(nodecontract.AdminNodeDisconnectRoute), handler.DisconnectNode)
}

func (handler *NodeEnrollmentHandler) RegisterBootstrapRoutes(group *gin.RouterGroup) {
	group.POST(bootstrapRoute(nodecontract.NodeEnrollmentClaimRoute), handler.Claim)
	group.GET(bootstrapRoute(nodecontract.NodeEnrollmentRequestRoute), handler.RequestStatus)
	group.POST(bootstrapRoute(nodecontract.NodeEnrollmentRequestCompleteRoute), handler.CompleteRequest)
}

type createEnrollmentRequest struct {
	Name             string            `json:"name" binding:"required"`
	Environment      string            `json:"environment"`
	Region           string            `json:"region"`
	Labels           map[string]string `json:"labels"`
	ExpectedNodeUUID string            `json:"expected_node_uuid"`
	RecoveryServerID uint64            `json:"recovery_server_id"`
	ExpiresInMinutes int               `json:"expires_in_minutes"`
	MaxUses          int               `json:"max_uses"`
	AutoApprove      bool              `json:"auto_approve"`
}

func (handler *NodeEnrollmentHandler) CreateEnrollment(c *gin.Context) {
	var request createEnrollmentRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid enrollment invitation: "+err.Error())
		return
	}
	result, err := handler.service.CreateEnrollment(service.CreateEnrollmentInput{
		Name: request.Name, Environment: request.Environment, Region: request.Region, Labels: request.Labels,
		ExpectedNodeUUID: request.ExpectedNodeUUID, RecoveryServerID: request.RecoveryServerID,
		ExpiresIn: time.Duration(request.ExpiresInMinutes) * time.Minute, MaxUses: request.MaxUses,
		AutoApprove: request.AutoApprove, Actor: adminActor(c), SourceIP: c.ClientIP(),
	})
	if err != nil {
		handleEnrollmentError(c, err)
		return
	}
	created(c, "node enrollment invitation created; token is shown once", result)
}

func (handler *NodeEnrollmentHandler) ListEnrollments(c *gin.Context) {
	records, err := handler.service.ListEnrollments()
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to list node enrollment invitations")
		return
	}
	success(c, "ok", records)
}

func (handler *NodeEnrollmentHandler) RevokeEnrollment(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		badRequest(c, ErrCodeParamInvalid, "invalid enrollment invitation id")
		return
	}
	if err := handler.service.RevokeEnrollment(id, adminActor(c), c.ClientIP()); err != nil {
		handleEnrollmentError(c, err)
		return
	}
	success(c, "node enrollment invitation revoked", nil)
}

func (handler *NodeEnrollmentHandler) DeleteEnrollment(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		badRequest(c, ErrCodeParamInvalid, "invalid enrollment invitation id")
		return
	}
	if err := handler.service.DeleteEnrollment(id, adminActor(c), c.ClientIP()); err != nil {
		handleEnrollmentError(c, err)
		return
	}
	success(c, "node enrollment invitation deleted", nil)
}

func (handler *NodeEnrollmentHandler) ListRequests(c *gin.Context) {
	records, err := handler.service.ListRequests(c.Query("state"))
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to list node enrollment requests")
		return
	}
	success(c, "ok", records)
}

func (handler *NodeEnrollmentHandler) GetRequest(c *gin.Context) {
	details, err := handler.service.GetRequest(c.Param("id"))
	if err != nil {
		handleEnrollmentError(c, err)
		return
	}
	success(c, "ok", details)
}

type reviewEnrollmentRequest struct {
	Note string `json:"note"`
}

func (handler *NodeEnrollmentHandler) ApproveRequest(c *gin.Context) {
	var request reviewEnrollmentRequest
	if err := c.ShouldBindJSON(&request); err != nil && !errors.Is(err, http.ErrBodyReadAfterClose) {
		badRequest(c, ErrCodeParamInvalid, "invalid approval request")
		return
	}
	approved, err := handler.service.ApproveRequest(c.Param("id"), adminActor(c), request.Note, c.ClientIP())
	if err != nil {
		handleEnrollmentError(c, err)
		return
	}
	success(c, "node enrollment approved", approved)
}

func (handler *NodeEnrollmentHandler) RejectRequest(c *gin.Context) {
	var request reviewEnrollmentRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid rejection request")
		return
	}
	if err := handler.service.RejectRequest(c.Param("id"), adminActor(c), request.Note, c.ClientIP()); err != nil {
		handleEnrollmentError(c, err)
		return
	}
	success(c, "node enrollment rejected", nil)
}

func (handler *NodeEnrollmentHandler) ListCredentials(c *gin.Context) {
	serverID, ok := serverIDParam(c)
	if !ok {
		return
	}
	records, err := handler.service.ListCredentials(serverID)
	if err != nil {
		serverError(c, ErrCodeInternal, "failed to list node credentials")
		return
	}
	success(c, "ok", records)
}

func (handler *NodeEnrollmentHandler) RotateCredential(c *gin.Context) {
	serverID, ok := serverIDParam(c)
	if !ok {
		return
	}
	credential, record, err := handler.service.RotateCredential(serverID, adminActor(c), c.ClientIP())
	if err != nil {
		handleEnrollmentError(c, err)
		return
	}
	handler.reconcileCredentialSessions(serverID)
	success(c, "node credential rotated; credential is shown once", gin.H{"credential": credential, "metadata": record})
}

func (handler *NodeEnrollmentHandler) reconcileCredentialSessions(serverID uint64) {
	if handler.sessionRevoker == nil {
		return
	}
	expirer, canExpire := handler.sessionRevoker.(credentialSessionExpirer)
	revoker, canRevoke := handler.sessionRevoker.(credentialSessionRevoker)
	if !canExpire || !canRevoke {
		handler.sessionRevoker.DisconnectServer(serverID, errors.New("node credential rotated; reconnect required"))
		return
	}
	credentials, err := handler.service.ListCredentials(serverID)
	if err != nil {
		handler.sessionRevoker.DisconnectServer(serverID, errors.New("node credential rotated; reconnect required"))
		return
	}
	for _, credential := range credentials {
		switch credential.State {
		case model.NodeCredentialRotating:
			if credential.ExpiresAt == nil {
				continue
			}
			expirer.ExpireCredentialAt(serverID, credential.ID, *credential.ExpiresAt)
		case model.NodeCredentialRevoked, model.NodeCredentialExpired:
			revoker.DisconnectCredential(serverID, credential.ID, errors.New("node credential revoked during rotation"))
		}
	}
}

func (handler *NodeEnrollmentHandler) RevokeCredentials(c *gin.Context) {
	serverID, ok := serverIDParam(c)
	if !ok {
		return
	}
	if err := handler.service.RevokeCredentials(serverID, adminActor(c), c.ClientIP()); err != nil {
		handleEnrollmentError(c, err)
		return
	}
	if handler.sessionRevoker != nil {
		handler.sessionRevoker.DisconnectServer(serverID, errors.New("node credential revoked"))
	}
	success(c, "node credentials revoked", nil)
}

func (handler *NodeEnrollmentHandler) RevokeCredential(c *gin.Context) {
	serverID, ok := serverIDParam(c)
	if !ok {
		return
	}
	credentialID, ok := credentialIDParam(c)
	if !ok {
		return
	}
	if err := handler.service.RevokeCredential(serverID, credentialID, adminActor(c), c.ClientIP()); err != nil {
		handleEnrollmentError(c, err)
		return
	}
	if handler.sessionRevoker != nil {
		if revoker, ok := handler.sessionRevoker.(credentialSessionRevoker); ok {
			revoker.DisconnectCredential(serverID, credentialID, errors.New("node credential rotation overlap ended"))
		} else {
			handler.sessionRevoker.DisconnectServer(serverID, errors.New("node credential rotation overlap ended"))
		}
	}
	success(c, "node credential rotation overlap ended", nil)
}

func (handler *NodeEnrollmentHandler) DeleteCredential(c *gin.Context) {
	serverID, ok := serverIDParam(c)
	if !ok {
		return
	}
	credentialID, ok := credentialIDParam(c)
	if !ok {
		return
	}
	if err := handler.service.DeleteCredential(serverID, credentialID, adminActor(c), c.ClientIP()); err != nil {
		handleEnrollmentError(c, err)
		return
	}
	success(c, "node credential deleted", nil)
}

func (handler *NodeEnrollmentHandler) DisconnectNode(c *gin.Context) {
	serverID, ok := serverIDParam(c)
	if !ok {
		return
	}
	if handler.sessionRevoker == nil || !handler.sessionRevoker.DisconnectServer(serverID, errors.New("node control session disconnected by administrator")) {
		fail(c, http.StatusConflict, ErrCodeBusiness, "node does not have an active control session")
		return
	}
	success(c, "node control session disconnected; the agent may reconnect automatically", nil)
}

type claimEnrollmentRequest struct {
	Token              string `json:"token" binding:"required"`
	NodeUUID           string `json:"node_uuid" binding:"required"`
	Name               string `json:"name" binding:"required"`
	Hostname           string `json:"hostname"`
	OS                 string `json:"os"`
	Arch               string `json:"arch"`
	AgentVersion       string `json:"agent_version"`
	MachineFingerprint string `json:"machine_fingerprint"`
}

func (handler *NodeEnrollmentHandler) Claim(c *gin.Context) {
	var request claimEnrollmentRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequest(c, ErrCodeParamInvalid, "invalid enrollment claim")
		return
	}
	result, err := handler.service.Claim(service.EnrollmentClaimInput{
		Token: request.Token, NodeUUID: request.NodeUUID, Name: request.Name, Hostname: request.Hostname,
		OS: request.OS, Arch: request.Arch, AgentVersion: request.AgentVersion,
		MachineFingerprint: request.MachineFingerprint, SourceIP: c.ClientIP(),
	})
	if err != nil {
		handleEnrollmentError(c, err)
		return
	}
	created(c, "node enrollment request submitted; request secret is shown once", result)
}

func (handler *NodeEnrollmentHandler) RequestStatus(c *gin.Context) {
	requestSecret, ok := requestSecret(c)
	if !ok {
		return
	}
	request, err := handler.service.RequestStatus(c.Param("id"), requestSecret)
	if err != nil {
		handleEnrollmentError(c, err)
		return
	}
	success(c, "ok", request)
}

func (handler *NodeEnrollmentHandler) CompleteRequest(c *gin.Context) {
	requestSecret, ok := requestSecret(c)
	if !ok {
		return
	}
	credential, metadata, err := handler.service.CompleteRequest(c.Param("id"), requestSecret, c.ClientIP())
	if err != nil {
		handleEnrollmentError(c, err)
		return
	}
	success(c, "node enrollment completed; credential is shown once", gin.H{"credential": credential, "metadata": metadata})
}

func requestSecret(c *gin.Context) (string, bool) {
	header := strings.TrimSpace(c.GetHeader("Authorization"))
	const scheme = "Request "
	if !strings.HasPrefix(header, scheme) || strings.TrimSpace(strings.TrimPrefix(header, scheme)) == "" {
		fail(c, http.StatusUnauthorized, ErrCodeUnauthorized, "expected Authorization: Request <request-secret>")
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(header, scheme)), true
}

func adminActor(c *gin.Context) string {
	if value, ok := c.Get("admin_user"); ok {
		if actor, valid := value.(string); valid && strings.TrimSpace(actor) != "" {
			return strings.TrimSpace(actor)
		}
	}
	return "admin"
}

func adminRoute(path string) string {
	return strings.TrimPrefix(path, "/api/v1/admin")
}

func bootstrapRoute(path string) string {
	return strings.TrimPrefix(path, "/api/v1/node-enrollments")
}

func serverIDParam(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		badRequest(c, ErrCodeParamInvalid, "invalid server id")
		return 0, false
	}
	return id, true
}

func credentialIDParam(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("credential_id"), 10, 64)
	if err != nil || id == 0 {
		badRequest(c, ErrCodeParamInvalid, "invalid node credential id")
		return 0, false
	}
	return id, true
}

func handleEnrollmentError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrNodeCredentialNotFound):
		notFound(c, "node credential not found")
	case errors.Is(err, service.ErrEnrollmentNotFound):
		notFound(c, "node enrollment record not found")
	case errors.Is(err, service.ErrEnrollmentTokenInvalid), errors.Is(err, service.ErrEnrollmentRequestSecret), errors.Is(err, service.ErrNodeCredentialInvalid):
		fail(c, http.StatusUnauthorized, ErrCodeInvalidToken, err.Error())
	case errors.Is(err, service.ErrEnrollmentTokenExpired):
		fail(c, http.StatusGone, ErrCodeBusiness, err.Error())
	case errors.Is(err, service.ErrEnrollmentTokenUnavailable), errors.Is(err, service.ErrEnrollmentUUIDMismatch),
		errors.Is(err, service.ErrEnrollmentDuplicateUUID), errors.Is(err, service.ErrEnrollmentInvalidState),
		errors.Is(err, service.ErrNodeCredentialInvalidState):
		fail(c, http.StatusConflict, ErrCodeBusiness, err.Error())
	default:
		if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "must") || strings.Contains(err.Error(), "invalid UUID") || strings.Contains(err.Error(), "expiration") {
			badRequest(c, ErrCodeParamInvalid, err.Error())
			return
		}
		serverError(c, ErrCodeInternal, "node enrollment operation failed")
	}
}
