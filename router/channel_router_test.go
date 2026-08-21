package router

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelStatusRoutesUseOperatePermission(t *testing.T) {
	assertChannelRoutePermission(t, http.MethodPost, "/:id/status", authz.ChannelOperate, controller.UpdateChannelStatus)
	assertChannelRoutePermission(t, http.MethodPost, "/status/batch", authz.ChannelOperate, controller.BatchUpdateChannelStatus)
	assertChannelRoutePermission(t, http.MethodPut, "/", authz.ChannelWrite, controller.UpdateChannel)
}

func TestChannelDeleteRoutesUseSensitiveWritePermission(t *testing.T) {
	assertChannelRoutePermission(t, http.MethodDelete, "/:id", authz.ChannelSensitiveWrite, controller.DeleteChannel)
	assertChannelRoutePermission(t, http.MethodPost, "/batch", authz.ChannelSensitiveWrite, controller.DeleteChannelBatch)
	assertChannelRoutePermission(t, http.MethodDelete, "/disabled", authz.ChannelSensitiveWrite, controller.DeleteDisabledChannel)
	assertChannelRoutePermission(t, http.MethodPut, "/", authz.ChannelWrite, controller.UpdateChannel)
	assertChannelRoutePermission(t, http.MethodPut, "/tag", authz.ChannelWrite, controller.EditTagChannels)
	assertChannelRoutePermission(t, http.MethodPost, "/batch/tag", authz.ChannelWrite, controller.BatchSetChannelTag)
}

func TestVeridropRoutesUseIndependentDynamicPermissions(t *testing.T) {
	viewRoutes := []struct {
		method  string
		path    string
		handler gin.HandlerFunc
	}{
		{http.MethodGet, "/targets", controller.ListChannelVeridropDetectionTargets},
		{http.MethodGet, "/results", controller.ListChannelVeridropDetectionResults},
		{http.MethodGet, "/results/:id", controller.GetChannelVeridropDetectionResult},
		{http.MethodGet, "/tasks", controller.ListVeridropSystemTasks},
	}
	for _, route := range viewRoutes {
		assertVeridropRoutePermission(t, route.method, route.path, authz.AdminMenuVeridropDetectionView, route.handler)
	}

	editRoutes := []struct {
		method  string
		path    string
		handler gin.HandlerFunc
	}{
		{http.MethodPost, "/detect", controller.StartChannelVeridropDetection},
		{http.MethodPost, "/detect_manual", controller.StartManualChannelVeridropDetection},
		{http.MethodPost, "/detect_enabled", controller.StartEnabledChannelsVeridropDetection},
		{http.MethodPost, "/detect_batch", controller.StartChannelsVeridropDetection},
		{http.MethodPost, "/manual_models", controller.FetchVeridropManualModels},
		{http.MethodPost, "/results/cleanup", controller.StartChannelVeridropDetectionCleanup},
	}
	for _, route := range editRoutes {
		assertVeridropRoutePermission(t, route.method, route.path, authz.AdminMenuVeridropDetectionEdit, route.handler)
	}
}

func TestChannelStatusRoutesRegisterWithoutConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api")

	require.NotPanics(t, func() {
		registerChannelRoutes(api)
	})
}

func assertChannelRoutePermission(t *testing.T, method string, path string, permission authz.Permission, handler any) {
	t.Helper()
	for _, route := range channelPermissionRoutes {
		if route.method == method && route.path == path {
			assert.Equal(t, permission, route.permission)
			assert.Equal(t, reflect.ValueOf(handler).Pointer(), reflect.ValueOf(route.handler).Pointer())
			return
		}
	}
	t.Fatalf("route %s %s not found", method, path)
}

func assertVeridropRoutePermission(t *testing.T, method string, path string, permission authz.Permission, handler any) {
	t.Helper()
	for _, route := range veridropChannelPermissionRoutes {
		if route.method == method && route.path == path {
			assert.Equal(t, permission, route.permission)
			assert.Equal(t, reflect.ValueOf(handler).Pointer(), reflect.ValueOf(route.handler).Pointer())
			return
		}
	}
	t.Fatalf("veridrop route %s %s not found", method, path)
}
