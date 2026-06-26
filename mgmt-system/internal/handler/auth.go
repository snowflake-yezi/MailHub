package handler

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/middleware"
)

type AuthHandler struct {
	adminUser string
	adminPass string
	sessions  *middleware.SessionManager
}

func NewAuthHandler(adminUser, adminPass string, sm *middleware.SessionManager) *AuthHandler {
	return &AuthHandler{
		adminUser: adminUser,
		adminPass: adminPass,
		sessions:  sm,
	}
}

// LoginPage renders the login form.
func (h *AuthHandler) LoginPage(c *gin.Context) {
	// Already logged in? Redirect to admin dashboard.
	token, _ := c.Cookie(middleware.SessionCookieName)
	if s := h.sessions.ValidateSession(token); s != nil {
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

	token, err := h.sessions.CreateSession(username)
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
		middleware.SessionCookieName,
		token,
		int(middleware.SessionDuration.Seconds()),
		"/", // path (covers /admin/* pages AND /api/v1/admin/* APIs)
		"",       // domain (auto)
		false,    // secure (set to true if using HTTPS)
		true,     // httpOnly
	)

	redirectURL := "/admin/"
	if next != "" {
		redirectURL = next
	}
	c.Redirect(http.StatusFound, redirectURL)
}

// LogoutAction destroys the session and redirects to login.
func (h *AuthHandler) LogoutAction(c *gin.Context) {
	token, _ := c.Cookie(middleware.SessionCookieName)
	if token != "" {
		h.sessions.DestroySession(token)
	}

	// Clear cookie.
	c.SetCookie(middleware.SessionCookieName, "", -1, "/", "", false, true)
	c.Redirect(http.StatusFound, "/admin/login")
}
