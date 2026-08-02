package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
	require.Contains(t, response.Body.String(), `"state":"disabled"`)
}
