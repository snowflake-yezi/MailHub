package handler

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/middleware"
)

type AuthHandler struct {
	adminUser    string
	adminPass    string
	sessionMgr   *middleware.SessionManager
	cookieSecure bool
}

func NewAuthHandler(adminUser, adminPass string, sm *middleware.SessionManager, cookieSecure bool) *AuthHandler {
	return &AuthHandler{
		adminUser:    adminUser,
		adminPass:    adminPass,
		sessionMgr:   sm,
		cookieSecure: cookieSecure,
	}
}

// LoginPage renders the login form.
func (h *AuthHandler) LoginPage(c *gin.Context) {
	// Already logged in? Redirect to admin dashboard.
	token, _ := c.Cookie(h.sessionMgr.CookieName())
	if s := h.sessionMgr.ValidateSession(token); s != nil {
		c.Redirect(http.StatusFound, "/admin/")
		return
	}

	next := c.Query("next")
	c.HTML(http.StatusOK, "login.html", gin.H{
		"title": "管理后台登录",
		"next":  next,
		"error": "",
	})
}

// LoginAction processes the login form submission.
func (h *AuthHandler) LoginAction(c *gin.Context) {
	username := c.PostForm("username")
	password := c.PostForm("password")
	next := c.PostForm("next")

	// Constant-time comparison to prevent timing attacks.
	userMatch := subtle.ConstantTimeCompare([]byte(username), []byte(h.adminUser)) == 1
	passMatch := subtle.ConstantTimeCompare([]byte(password), []byte(h.adminPass)) == 1

	if !userMatch || !passMatch {
		c.HTML(http.StatusUnauthorized, "login.html", gin.H{
			"title": "管理后台登录",
			"next":  next,
			"error": "用户名或密码错误",
		})
		return
	}

	token, err := h.sessionMgr.CreateSession(username)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{
			"title": "管理后台登录",
			"next":  next,
			"error": "创建会话失败，请重试",
		})
		return
	}

	// Set session cookie.
	c.SetCookie(
		h.sessionMgr.CookieName(),
		token,
		int(h.sessionMgr.Duration().Seconds()),
		"/", // path (covers /admin/* pages AND /api/v1/admin/* APIs)
		"",     // domain (auto)
		h.cookieSecure,
		true,   // httpOnly
	)

	redirectURL := "/admin/"
	if next != "" {
		redirectURL = next
	}
	c.Redirect(http.StatusFound, redirectURL)
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
