package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/sidero/capabilities"
	"github.com/pterodactyl/wings/server"
)

func TestSideroCapabilitiesUsesNodeAuthentication(t *testing.T) {
	c, err := config.NewAtPath("test.yml")
	require.NoError(t, err)
	c.AuthenticationToken = "secret"
	c.Token.Token = "secret"
	config.Set(c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	router := ConfigureWithContext(ctx, server.NewEmptyManager(nil), nil)

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/sidero/v1/capabilities", nil))
	require.Equal(t, http.StatusUnauthorized, unauthorized.Code)
	request := httptest.NewRequest(http.MethodGet, "/api/sidero/v1/capabilities", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	var payload capabilities.Response
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Equal(t, 1, payload.ProtocolVersion)
	require.NotContains(t, response.Body.String(), "secret")
}

func TestSideroDisabledCapabilityEndpointRemainsAvailable(t *testing.T) {
	c, err := config.NewAtPath("test.yml")
	require.NoError(t, err)
	c.AuthenticationToken = "secret"
	c.Token.Token = "secret"
	c.Sidero.Enabled = false
	config.Set(c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	router := ConfigureWithContext(ctx, server.NewEmptyManager(nil), nil)
	request := httptest.NewRequest(http.MethodGet, "/api/sidero/v1/capabilities", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	var capabilitiesPayload capabilities.Response
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &capabilitiesPayload))
	require.False(t, capabilitiesPayload.Features["resolved_modpack"])
	require.False(t, capabilitiesPayload.Features["archive_inspection"])
	require.False(t, capabilitiesPayload.Features["safe_extraction"])

	healthRequest := httptest.NewRequest(http.MethodGet, "/api/sidero/v1/health", nil)
	healthRequest.Header.Set("Authorization", "Bearer secret")
	healthResponse := httptest.NewRecorder()
	router.ServeHTTP(healthResponse, healthRequest)
	require.Equal(t, http.StatusOK, healthResponse.Code)
	var healthPayload struct {
		State      string            `json:"state"`
		Components map[string]string `json:"components"`
	}
	require.NoError(t, json.Unmarshal(healthResponse.Body.Bytes(), &healthPayload))
	require.Equal(t, "disabled", healthPayload.State)
	require.Equal(t, "disabled", healthPayload.Components["resolved_modpack"])
	require.Equal(t, "disabled", healthPayload.Components["archive_inspection"])
	require.Equal(t, "disabled", healthPayload.Components["safe_extraction"])
	for name := range capabilitiesPayload.Features {
		require.Equalf(t, "disabled", healthPayload.Components[name], "component %s should be disabled", name)
	}
}

func TestSideroHealthMarksOperationCapabilitiesUnavailableWithoutManager(t *testing.T) {
	c, err := config.NewAtPath("test.yml")
	require.NoError(t, err)
	c.AuthenticationToken = "secret"
	c.Token.Token = "secret"
	config.Set(c)

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	(&sideroExtension{}).health(ctx)

	var payload struct {
		Components map[string]string `json:"components"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	for _, name := range []string{
		"archive_support",
		"file_search",
		"content_search",
		"remote_download",
		"archive_inspection",
		"safe_extraction",
		"installer_service",
		"installer_operations",
		"resolved_modpack",
		"world_operations",
	} {
		require.Equalf(t, "unavailable", payload.Components[name], "component %s should be unavailable without an operation manager", name)
	}
}
