package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// API 统一响应格式
type Response struct {
	Code      int         `json:"code"`
	Message   string      `json:"message"`
	Data      interface{} `json:"data,omitempty"`
	RequestID string      `json:"request_id"`
}

func success(c *gin.Context, msg string, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code:      0,
		Message:   msg,
		Data:      data,
		RequestID: uuid.New().String()[:8],
	})
}

func created(c *gin.Context, msg string, data interface{}) {
	c.JSON(http.StatusCreated, Response{
		Code:      0,
		Message:   msg,
		Data:      data,
		RequestID: uuid.New().String()[:8],
	})
}

func badRequest(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusBadRequest, Response{
		Code:      code,
		Message:   msg,
		RequestID: uuid.New().String()[:8],
	})
}

func serverError(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusInternalServerError, Response{
		Code:      code,
		Message:   msg,
		RequestID: uuid.New().String()[:8],
	})
}

func notFound(c *gin.Context, msg string) {
	c.JSON(http.StatusNotFound, Response{
		Code:      2003,
		Message:   msg,
		RequestID: uuid.New().String()[:8],
	})
}

// 错误码
const (
	ErrCodeParamMissing   = 1001
	ErrCodeParamInvalid   = 1002
	ErrCodeUnauthorized   = 1003
	ErrCodeInvalidToken   = 1004
	ErrCodeInsufficientScope = 1005

	ErrCodeNoServer       = 2001
	ErrCodeServerCreate   = 2002
	ErrCodeNotFound       = 2003
	ErrCodeBusiness       = 2004 // 业务规则限制（如：服务器有邮箱不能删除）

	ErrCodeExternalFail   = 3001

	ErrCodeInternal       = 5000
)
