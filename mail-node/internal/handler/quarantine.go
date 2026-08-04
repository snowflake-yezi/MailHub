package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mail-node/internal/filterquarantine"
	"github.com/ticket/email-mail-node/internal/mailparse"
)

func (h *NodeHandler) GetQuarantineMessage(c *gin.Context) {
	if h.quarantine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 5000, "message": "quarantine store unavailable"})
		return
	}
	metadata, path, err := h.quarantine.MessagePath(c.Param("quarantine_key"))
	if err != nil {
		h.writeQuarantineError(c, err)
		return
	}
	message, err := parseFullMessage(path, metadata.Mailbox, h.mailboxMgr.MaildirBase())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": "failed to parse quarantined message"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"metadata": metadata, "message": message}})
}

func (h *NodeHandler) GetQuarantineAttachment(c *gin.Context) {
	if h.quarantine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 5000, "message": "quarantine store unavailable"})
		return
	}
	_, path, err := h.quarantine.MessagePath(c.Param("quarantine_key"))
	if err != nil {
		h.writeQuarantineError(c, err)
		return
	}
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil || index < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1002, "message": "invalid attachment index"})
		return
	}
	part, info, inline, err := attachmentPartFromPath(path, index)
	if err != nil {
		if errors.Is(err, mailparse.ErrMIMETooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"code": 1003, "message": "message too large"})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"code": 2003, "message": err.Error()})
		return
	}
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	c.Header("Content-Disposition", contentDisposition(disposition, info.Filename))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, info.ContentType, part.Content)
}

func attachmentPartFromPath(path string, index int) (*parsedPart, inferredPartInfo, bool, error) {
	part, err := mailparse.ParseAttachment(path, index)
	if err != nil {
		return nil, inferredPartInfo{}, false, err
	}
	return part, part.Info, part.Inline, nil
}

func (h *NodeHandler) ReleaseQuarantine(c *gin.Context) {
	if h.quarantine == nil || h.quarantineRelease == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 5000, "message": "quarantine release unavailable"})
		return
	}
	var request struct {
		OperationID string `json:"operation_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "operation_id is required"})
		return
	}
	receipt, err := h.quarantine.Release(c.Param("quarantine_key"), request.OperationID, h.quarantineRelease)
	if err != nil && receipt == nil {
		h.writeQuarantineError(c, err)
		return
	}
	status := http.StatusOK
	if err != nil {
		status = http.StatusBadGateway
	}
	c.JSON(status, gin.H{"code": 0, "message": receipt.Status, "data": receipt})
}

func (h *NodeHandler) GetQuarantineReleaseStatus(c *gin.Context) {
	if h.quarantine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 5000, "message": "quarantine store unavailable"})
		return
	}
	receipt, err := h.quarantine.Receipt(c.Param("quarantine_key"))
	if err != nil {
		h.writeQuarantineError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": receipt})
}

func (h *NodeHandler) PurgeExpiredQuarantines(c *gin.Context) {
	if h.quarantine == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 5000, "message": "quarantine store unavailable"})
		return
	}
	var request struct {
		RetentionDays int `json:"retention_days" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.RetentionDays <= 0 || request.RetentionDays > 36500 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1002, "message": "retention_days must be between 1 and 36500"})
		return
	}
	keys, err := h.quarantine.PurgeExpired(time.Now().Add(-time.Duration(request.RetentionDays) * 24 * time.Hour))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": "quarantine gc failed: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "data": gin.H{"expired_keys": keys}})
}

func (h *NodeHandler) writeQuarantineError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, filterquarantine.ErrInvalidKey), errors.Is(err, filterquarantine.ErrInvalidPath):
		c.JSON(http.StatusBadRequest, gin.H{"code": 1002, "message": err.Error()})
	case errors.Is(err, filterquarantine.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"code": 2003, "message": "quarantine not found"})
	case errors.Is(err, filterquarantine.ErrOperationConflict):
		c.JSON(http.StatusConflict, gin.H{"code": 2004, "message": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
	}
}
