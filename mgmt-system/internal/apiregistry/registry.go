package apiregistry

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ticket/email-mgmt-system/internal/middleware"
	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/store"
)

type Route struct {
	Method         string
	Path           string
	PermissionCode string
	GroupName      string
	Name           string
	ResourceName   string
	Description    string
	SortOrder      int
	Handler        gin.HandlerFunc
}

type Registry struct {
	basePath    string
	permissions map[string]model.APIPermission
	resources   []model.APIResource
}

func New(basePath string) *Registry {
	return &Registry{basePath: strings.TrimRight(basePath, "/"), permissions: make(map[string]model.APIPermission)}
}

// Register binds the route, permission check and persisted metadata in one operation.
func (r *Registry) Register(group *gin.RouterGroup, route Route) {
	if route.Method == "" || route.Path == "" || route.PermissionCode == "" || route.Handler == nil {
		panic("external API route requires method, path, permission and handler")
	}
	if len(r.basePath+route.Path) > 175 {
		panic("external API route path exceeds the 175-character database limit")
	}
	group.Handle(route.Method, route.Path, middleware.RequirePermission(route.PermissionCode), route.Handler)
	r.permissions[route.PermissionCode] = model.APIPermission{
		Code: route.PermissionCode, GroupName: route.GroupName, Name: route.Name,
		Description: route.Description, SortOrder: route.SortOrder, Active: true,
	}
	resourceName := route.ResourceName
	if resourceName == "" {
		resourceName = route.Name
	}
	r.resources = append(r.resources, model.APIResource{
		Method: route.Method, Path: r.basePath + route.Path, PermissionCode: route.PermissionCode,
		Name: resourceName, Status: "active",
	})
}

func (r *Registry) Sync(s *store.Store) error {
	permissions := make([]model.APIPermission, 0, len(r.permissions))
	for _, permission := range r.permissions {
		permissions = append(permissions, permission)
	}
	if err := s.SyncAPIRegistry(permissions, r.resources); err != nil {
		return fmt.Errorf("sync external API registry: %w", err)
	}
	return nil
}
