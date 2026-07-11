package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/middleware"
	"github.com/ticket/email-mgmt-system/internal/service"
)

type AuthHandler struct {
	credentials  *service.AdminCredentialService
	sessionMgr   *middleware.SessionManager
	cookieSecure bool
}

func NewAuthHandler(credentials *service.AdminCredentialService, sm *middleware.SessionManager, cookieSecure bool) *AuthHandler {
	return &AuthHandler{
		credentials:  credentials,
		sessionMgr:   sm,
		cookieSecure: cookieSecure,
	}
}

// LoginPage renders the login form.
func (h *AuthHandler) LoginPage(c *gin.Context) {
	// Already logged in? Redirect to admin dashboard.
	token, _ := c.Cookie(h.sessionMgr.CookieName())
	if session := h.sessionMgr.ValidateSession(token); session != nil {
		if user, err := h.credentials.GetByID(session.AdminUserID); err == nil && user.Status == "active" && user.CredentialVersion == session.CredentialVersion {
			c.Redirect(http.StatusFound, "/admin/")
			return
		}
		h.sessionMgr.DestroySession(token)
	}

	next := safeAdminRedirect(c.Query("next"))
	bootstrapped, _ := h.credentials.IsBootstrapped()
	c.HTML(http.StatusOK, "login.html", gin.H{
		"title":       "管理后台登录",
		"next":        next,
		"error":       "",
		"initialized": bootstrapped,
		"username":    "",
	})
}

// LoginAction processes the login form submission.
func (h *AuthHandler) LoginAction(c *gin.Context) {
	username := c.PostForm("username")
	password := c.PostForm("password")
	next := safeAdminRedirect(c.PostForm("next"))
	bootstrapped, bootstrapErr := h.credentials.IsBootstrapped()
	if bootstrapErr != nil || !bootstrapped {
		c.HTML(http.StatusServiceUnavailable, "login.html", gin.H{
			"title": "管理后台登录", "next": next, "username": username,
			"initialized": false, "error": "系统尚未初始化，请联系部署管理员",
		})
		return
	}

	user, err := h.credentials.Verify(username, password)
	if err != nil {
		if !errors.Is(err, service.ErrInvalidCredentials) {
			c.HTML(http.StatusInternalServerError, "login.html", gin.H{
				"title": "管理后台登录", "next": next, "username": username,
				"initialized": true, "error": "暂时无法登录，请稍后重试",
			})
			return
		}
		c.HTML(http.StatusUnauthorized, "login.html", gin.H{
			"title":       "管理后台登录",
			"next":        next,
			"error":       "用户名或密码错误",
			"initialized": true,
			"username":    username,
		})
		return
	}

	token, err := h.sessionMgr.CreateSession(user.ID, user.Username, user.CredentialVersion)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{
			"title":       "管理后台登录",
			"next":        next,
			"error":       "创建会话失败，请重试",
			"initialized": true,
			"username":    username,
		})
		return
	}

	// Set session cookie.
	c.SetCookie(
		h.sessionMgr.CookieName(),
		token,
		int(h.sessionMgr.Duration().Seconds()),
		"/", // path (covers /admin/* pages AND /api/v1/admin/* APIs)
		"",  // domain (auto)
		h.cookieSecure,
		true, // httpOnly
	)

	redirectURL := next
	if user.MustChangePassword {
		redirectURL = "/admin/config?account=required"
	} else if redirectURL == "" {
		redirectURL = "/admin/"
	}
	c.Redirect(http.StatusFound, redirectURL)
}

func safeAdminRedirect(next string) string {
	next = strings.TrimSpace(next)
	if (next == "/admin" || strings.HasPrefix(next, "/admin/") || strings.HasPrefix(next, "/admin?")) && !strings.HasPrefix(next, "//") {
		return next
	}
	return ""
}

// LogoutAction destroys the session and redirects to login.
func (h *AuthHandler) LogoutAction(c *gin.Context) {
	token, _ := c.Cookie(h.sessionMgr.CookieName())
	if token != "" {
		h.sessionMgr.DestroySession(token)
	}

	// Clear cookie.
	c.SetCookie(h.sessionMgr.CookieName(), "", -1, "/", "", h.cookieSecure, true)
	c.Redirect(http.StatusFound, "/admin/login")
}
