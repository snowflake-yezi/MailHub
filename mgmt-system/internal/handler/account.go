package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/service"
)

type AccountHandler struct {
	credentials *service.AdminCredentialService
}

func NewAccountHandler(credentials *service.AdminCredentialService) *AccountHandler {
	return &AccountHandler{credentials: credentials}
}

func (h *AccountHandler) Get(c *gin.Context) {
	userID, ok := adminUserID(c)
	if !ok {
		return
	}
	user, err := h.credentials.GetByID(userID)
	if err != nil {
		serverError(c, ErrCodeInternal, "读取管理账号失败")
		return
	}
	success(c, "ok", gin.H{
		"username":             user.Username,
		"must_change_password": user.MustChangePassword,
		"password_changed_at":  user.PasswordChangedAt,
	})
}

func (h *AccountHandler) Update(c *gin.Context) {
	userID, ok := adminUserID(c)
	if !ok {
		return
	}
	var req struct {
		Username        string `json:"username"`
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, ErrCodeParamInvalid, "请求格式错误")
		return
	}
	if req.CurrentPassword == "" {
		badRequest(c, ErrCodeParamMissing, "请输入当前密码")
		return
	}
	user, err := h.credentials.UpdateAccount(userID, service.AccountUpdate{
		Username: req.Username, CurrentPassword: req.CurrentPassword, NewPassword: req.NewPassword,
	})
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			c.JSON(http.StatusUnauthorized, Response{Code: ErrCodeUnauthorized, Message: "当前密码错误"})
			return
		}
		badRequest(c, ErrCodeBusiness, err.Error())
		return
	}
	success(c, "管理账号已更新，请重新登录", gin.H{"username": user.Username})
}

func adminUserID(c *gin.Context) (uint64, bool) {
	value, exists := c.Get("admin_user_id")
	id, ok := value.(uint64)
	if !exists || !ok || id == 0 {
		c.JSON(http.StatusUnauthorized, Response{Code: ErrCodeUnauthorized, Message: "authentication required"})
		return 0, false
	}
	return id, true
}
