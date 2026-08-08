package router

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apex/log"
	"github.com/gin-gonic/gin"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/models"
	"github.com/pterodactyl/wings/internal/sidero/archives"
	"github.com/pterodactyl/wings/internal/sidero/capabilities"
	siderocleanup "github.com/pterodactyl/wings/internal/sidero/cleanup"
	"github.com/pterodactyl/wings/internal/sidero/download"
	sideroerrors "github.com/pterodactyl/wings/internal/sidero/errors"
	"github.com/pterodactyl/wings/internal/sidero/files"
	"github.com/pterodactyl/wings/internal/sidero/firewall"
	"github.com/pterodactyl/wings/internal/sidero/installers"
	sideronetwork "github.com/pterodactyl/wings/internal/sidero/network"
	"github.com/pterodactyl/wings/internal/sidero/operations"
	"github.com/pterodactyl/wings/internal/sidero/query"
	"github.com/pterodactyl/wings/internal/sidero/ratelimit"
	"github.com/pterodactyl/wings/internal/sidero/uploads"
	"github.com/pterodactyl/wings/internal/sidero/worlds"
	"github.com/pterodactyl/wings/router/middleware"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/system"
)

type sideroExtension struct {
	operations             *operations.Manager
	uploads                *uploads.Manager
	cleanupRunning         atomic.Bool
	networkRunning         atomic.Bool
	firewallCleanupRunning atomic.Bool
	networkMu              sync.RWMutex
	network                map[string]*sideronetwork.Buffer
	queries                *query.Manager
	firewall               *firewall.Engine
	firewallStore          *firewall.FileStore
	firewallMu             sync.RWMutex
	firewallState          string
	rateLimiter            *ratelimit.Limiter
}

func registerSideroRoutes(ctx context.Context, protected gin.IRoutes, serverRoutes *gin.RouterGroup, servers *server.Manager) {
	cfg := config.Get().Sidero
	extension := &sideroExtension{network: map[string]*sideronetwork.Buffer{}}
	extension.operations = operations.NewManager(operations.Config{
		MaximumConcurrentGlobal:    cfg.Operations.MaximumConcurrentGlobal,
		MaximumConcurrentPerServer: cfg.Operations.MaximumConcurrentPerServer,
		Retention:                  time.Duration(cfg.Operations.RetentionSeconds) * time.Second,
		QueueSize:                  cfg.Operations.MaximumConcurrentGlobal * 4,
	}, func(event operations.Event) {
		if s, ok := servers.Get(event.ServerID); ok {
			s.Events().Publish(event.Name, event.Payload)
			if event.Name == "sidero operation completed" || event.Name == "sidero operation failed" || event.Name == "sidero operation cancelled" {
				metadata := models.ActivityMeta{"operation_id": event.Payload["operation_id"], "type": event.Payload["type"], "state": event.Payload["state"]}
				if code, exists := event.Payload["error_code"]; exists {
					metadata["error_code"] = code
				}
				s.SaveActivity(s.NewRequestActivity("", ""), models.Event("server:sidero.operation"), metadata)
			}
		}
	})
	extension.uploads = uploads.NewManager(uploads.Config{ChunkSize: cfg.Uploads.ChunkSize, SessionExpiry: time.Duration(cfg.Uploads.SessionExpirySeconds) * time.Second, MaximumUploadBytes: cfg.Uploads.MaximumUploadBytes, MaximumGlobal: cfg.Operations.MaximumConcurrentGlobal, MaximumPerServer: cfg.Uploads.MaximumConcurrentPerServer, Publish: func(serverID, event string, payload map[string]any) {
		if srv, ok := servers.Get(serverID); ok {
			srv.Events().Publish(event, payload)
		}
	}})
	extension.queries = query.NewManager(query.Config{Timeout: time.Duration(cfg.GameQuery.TimeoutMilliseconds) * time.Millisecond, Cache: time.Duration(cfg.GameQuery.CacheSeconds) * time.Second, Stale: time.Duration(cfg.GameQuery.StaleSeconds) * time.Second, OfflineBackoff: time.Duration(cfg.GameQuery.OfflineBackoffSeconds) * time.Second, MaximumRetries: cfg.GameQuery.MaximumRetries}, nil)
	extension.rateLimiter = ratelimit.New(time.Duration(cfg.RateLimits.WindowSeconds)*time.Second, cfg.RateLimits.MaximumGlobalPerRoute, cfg.RateLimits.MaximumPerServerPerRoute, cfg.RateLimits.MaximumTrackedServerRoute)
	extension.firewallStore = firewall.NewFileStore(filepath.Join(config.Get().System.RootDirectory, "sidero", "firewall-state.json"))
	extension.firewall = firewall.NewEngine(firewall.NewNftBackend(), cfg.Firewall.MaximumRulesPerServer, func(serverID, action, ruleID string) {
		if srv, ok := servers.Get(serverID); ok {
			srv.SaveActivity(srv.NewRequestActivity("", ""), models.Event("server:sidero.firewall."+action), models.ActivityMeta{"rule_id": ruleID})
		}
	})
	extension.firewall.SetStore(extension.firewallStore)
	extension.firewallState = "disabled"
	if cfg.Enabled && cfg.Firewall.Enabled {
		extension.restoreFirewall(ctx, servers)
		go extension.cleanupFirewall(ctx, time.Duration(cfg.Firewall.TemporaryRuleCleanupSeconds)*time.Second)
	}
	go extension.cleanup(ctx, time.Duration(cfg.Operations.CleanupIntervalSeconds)*time.Second, servers)
	if cfg.Enabled && cfg.NetworkStatistics.Enabled {
		go extension.sampleNetwork(ctx, servers, time.Duration(cfg.NetworkStatistics.SampleIntervalSeconds)*time.Second, cfg.NetworkStatistics.RecentSampleCount)
	}

	protected.GET("/api/sidero/v1/capabilities", func(c *gin.Context) {
		response := capabilities.Build(config.Get().Sidero, system.Version)
		response.Features["firewall"] = config.Get().Sidero.Enabled && config.Get().Sidero.Firewall.Enabled && extension.firewall.Available()
		c.JSON(http.StatusOK, response)
	})
	protected.GET("/api/sidero/v1/health", extension.health)

	sidero := serverRoutes.Group("/sidero")
	sidero.Use(extension.rateLimitMiddleware())
	sidero.GET("/operations", extension.listOperations)
	sidero.GET("/operations/:operation", extension.getOperation)
	sidero.DELETE("/operations/:operation", extension.cancelOperation)
	sidero.POST("/files/search", extension.search)
	sidero.POST("/files/content-search", extension.contentSearch)
	sidero.POST("/files/remote-download", extension.remoteDownload)
	sidero.POST("/files/archive/inspect", extension.inspectArchive)
	sidero.POST("/files/archive/extract", extension.extractArchive)
	sidero.POST("/files/probe", extension.probe)
	sidero.PUT("/files/conditional-write", extension.conditionalWrite)
	sidero.POST("/uploads", extension.createUpload)
	sidero.GET("/uploads/:upload", extension.getUpload)
	sidero.PUT("/uploads/:upload/chunks/:index", extension.putUploadChunk)
	sidero.POST("/uploads/:upload/complete", extension.completeUpload)
	sidero.DELETE("/uploads/:upload", extension.cancelUpload)
	sidero.GET("/network", extension.getNetwork)
	// Safety-critical services remain behind authenticated, server-scoped v1 routes.
	sidero.POST("/installers/execute", extension.executeInstaller)
	sidero.POST("/installers/modpack", extension.installModpack)
	sidero.GET("/query", extension.gameQuery)
	sidero.POST("/worlds/inspect", extension.worldInspect)
	sidero.POST("/worlds/import", extension.worldImport)
	sidero.POST("/worlds/archive", extension.worldArchive)
	sidero.POST("/worlds/clone", extension.worldClone)
	sidero.POST("/worlds/rename", extension.worldRename)
	sidero.POST("/worlds/replace", extension.worldReplace)
	sidero.POST("/worlds/size", extension.worldSize)
	sidero.GET("/firewall", extension.listFirewallRules)
	sidero.POST("/firewall/dry-run", extension.dryRunFirewallRule)
	sidero.POST("/firewall", extension.addFirewallRule)
	sidero.DELETE("/firewall/:rule", extension.removeFirewallRule)
}

func (s *sideroExtension) cleanupFirewall(ctx context.Context, interval time.Duration) {
	s.firewallCleanupRunning.Store(true)
	defer s.firewallCleanupRunning.Store(false)
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.firewall.CleanupExpired(ctx, now, 64)
		}
	}
}

func (s *sideroExtension) cleanup(ctx context.Context, interval time.Duration, servers *server.Manager) {
	s.cleanupRunning.Store(true)
	defer s.cleanupRunning.Store(false)
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer s.operations.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			operationsRemoved := s.operations.Cleanup(now, 256)
			uploadsRemoved := s.uploads.Cleanup(now, 64)
			firewallRemoved := s.firewall.CleanupExpired(ctx, now, 64)
			rateBucketsRemoved := s.rateLimiter.Cleanup(now, 256)
			queryCacheRemoved := s.queries.Cleanup(now, 256)
			stagingRemoved := 0
			activeServers := map[string]struct{}{}
			for _, srv := range servers.All() {
				activeServers[srv.ID()] = struct{}{}
				if stagingRemoved < 64 {
					stagingRemoved += siderocleanup.Staging(ctx, srv.Filesystem(), now, 7*24*time.Hour, 64-stagingRemoved)
				}
			}
			if config.Get().Sidero.Enabled && config.Get().Sidero.Firewall.Enabled && s.firewall.Available() {
				seen := map[string]struct{}{}
				for _, rule := range s.firewall.Snapshot() {
					if _, duplicate := seen[rule.ServerID]; duplicate {
						continue
					}
					seen[rule.ServerID] = struct{}{}
					if _, exists := activeServers[rule.ServerID]; !exists {
						_ = s.firewall.RemoveServer(ctx, rule.ServerID)
					}
				}
			}
			s.networkMu.Lock()
			for serverID := range s.network {
				if _, exists := activeServers[serverID]; !exists {
					delete(s.network, serverID)
				}
			}
			s.networkMu.Unlock()
			if operationsRemoved+uploadsRemoved+firewallRemoved+rateBucketsRemoved+queryCacheRemoved+stagingRemoved > 0 {
				log.WithFields(log.Fields{"operations": operationsRemoved, "uploads": uploadsRemoved, "firewall_rules": firewallRemoved, "rate_buckets": rateBucketsRemoved, "query_cache": queryCacheRemoved, "staging_directories": stagingRemoved}).Info("sidero cleanup completed")
			}
		}
	}
}

func (s *sideroExtension) rateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		category := sideroRouteCategory(c.Request.URL.Path)
		allowed, retryAfter := s.rateLimiter.Allow(category, middleware.ExtractServer(c).ID(), time.Now())
		if !allowed {
			seconds := int(retryAfter.Round(time.Second) / time.Second)
			if seconds < 1 {
				seconds = 1
			}
			c.Header("Retry-After", strconv.Itoa(seconds))
			respondSideroError(c, sideroerrors.New(sideroerrors.CodeOperationLimitReached, "The request rate limit has been reached.", http.StatusTooManyRequests).WithRetryable(true))
			return
		}
		c.Next()
	}
}

func sideroRouteCategory(route string) string {
	switch {
	case strings.Contains(route, "/content-search"):
		return "content_search"
	case strings.Contains(route, "/files/search"):
		return "file_search"
	case strings.Contains(route, "/remote-download"):
		return "remote_download"
	case strings.Contains(route, "/uploads"):
		return "uploads"
	case strings.Contains(route, "/installers"):
		return "installers"
	case strings.Contains(route, "/query"):
		return "game_query"
	case strings.Contains(route, "/archive"):
		return "archives"
	case strings.Contains(route, "/worlds"):
		return "worlds"
	case strings.Contains(route, "/firewall"):
		return "firewall"
	case strings.Contains(route, "/conditional-write"):
		return "conditional_write"
	case strings.Contains(route, "/probe"):
		return "file_probe"
	case strings.Contains(route, "/network"):
		return "network"
	default:
		return "operations"
	}
}

func (s *sideroExtension) restoreFirewall(ctx context.Context, servers *server.Manager) {
	state, err := s.firewallStore.Load()
	if err != nil {
		s.setFirewallState("misconfigured")
		log.WithField("error_code", sideroerrors.CodeFirewallApplyFailed).Warn("sidero firewall state could not be loaded")
		return
	}
	now := time.Now().UTC()
	rules := make([]firewall.AppliedRule, 0, len(state))
	policy := config.Get().Sidero.Firewall
	for _, persisted := range state {
		srv, ok := servers.Get(persisted.ServerID)
		if !ok {
			continue
		}
		allocation, ok := findSideroAllocation(srv, persisted.AllocationID)
		if !ok {
			continue
		}
		validated, err := firewall.Validate(persisted.ServerID, firewall.Request{AllocationID: persisted.AllocationID, Protocol: persisted.Protocol, Source: persisted.Source, Action: persisted.Action, ExpiresAt: persisted.ExpiresAt, Description: persisted.Description}, now)
		if err != nil || validated.ID != persisted.ID || !firewall.SourcePermitted(validated.Source, policy.AllowedSources, policy.BlockedSources) {
			continue
		}
		ip := net.ParseIP(allocation.IP)
		if ip == nil || allocation.Port < 1 || allocation.Port > 65535 {
			continue
		}
		candidate := firewall.AppliedRule{Rule: validated, ServerID: persisted.ServerID, Destination: ip.String(), Port: allocation.Port}
		if _, err := firewall.RenderRuleset([]firewall.AppliedRule{candidate}, false); err != nil {
			continue
		}
		rules = append(rules, candidate)
	}
	if err := s.firewall.Restore(rules); err != nil {
		s.setFirewallState("misconfigured")
		log.WithField("error_code", sideroerrors.CodeFirewallRuleInvalid).Warn("sidero firewall desired state is invalid")
		return
	}
	if !s.firewall.Available() {
		s.setFirewallState("unsupported")
		return
	}
	if err := s.firewall.Reconcile(ctx); err != nil {
		s.setFirewallState("degraded")
		log.WithField("error_code", sideroerrors.CodeFirewallApplyFailed).Warn("sidero firewall reconciliation failed")
		return
	}
	if err := s.firewallStore.Save(ctx, s.firewall.Snapshot()); err != nil {
		s.setFirewallState("degraded")
		log.WithField("error_code", sideroerrors.CodeFirewallApplyFailed).Warn("sidero firewall reconciled but state cleanup failed")
		return
	}
	s.setFirewallState("healthy")
}

func (s *sideroExtension) setFirewallState(state string) {
	s.firewallMu.Lock()
	s.firewallState = state
	s.firewallMu.Unlock()
}

func (s *sideroExtension) getFirewallState() string {
	s.firewallMu.RLock()
	defer s.firewallMu.RUnlock()
	return s.firewallState
}

func findSideroAllocation(srv *server.Server, id int64) (server.SideroAllocation, bool) {
	return srv.SideroAllocation(id)
}

func (s *sideroExtension) firewallEnabled(c *gin.Context) bool {
	cfg := config.Get().Sidero
	if !cfg.Enabled || !cfg.Firewall.Enabled {
		featureDisabled(c)
		return false
	}
	if !s.firewall.Available() {
		unsupportedCapability(c)
		return false
	}
	return true
}

func (s *sideroExtension) listFirewallRules(c *gin.Context) {
	if !s.firewallEnabled(c) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"rules": s.firewall.List(middleware.ExtractServer(c).ID())})
}

func (s *sideroExtension) dryRunFirewallRule(c *gin.Context) {
	s.firewallMutation(c, true)
}

func (s *sideroExtension) addFirewallRule(c *gin.Context) {
	s.firewallMutation(c, false)
}

func (s *sideroExtension) firewallMutation(c *gin.Context, dryRun bool) {
	if !s.firewallEnabled(c) {
		return
	}
	var request firewall.Request
	if c.ShouldBindJSON(&request) != nil {
		firewallRequestError(c, firewall.ErrInvalidRule)
		return
	}
	policy := config.Get().Sidero.Firewall
	if !firewall.SourcePermitted(request.Source, policy.AllowedSources, policy.BlockedSources) {
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeFirewallRuleInvalid, "The firewall source is denied by node policy.", http.StatusForbidden))
		return
	}
	srv := middleware.ExtractServer(c)
	allocation, ok := findSideroAllocation(srv, request.AllocationID)
	if !ok {
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeFirewallRuleInvalid, "The firewall allocation is not registered to this server.", http.StatusNotFound))
		return
	}
	ip := net.ParseIP(allocation.IP)
	if ip == nil || allocation.Port < 1 || allocation.Port > 65535 {
		firewallRequestError(c, firewall.ErrInvalidRule)
		return
	}
	var (
		rule firewall.AppliedRule
		err  error
	)
	if dryRun {
		rule, err = s.firewall.DryRun(c.Request.Context(), srv.ID(), ip.String(), allocation.Port, request, time.Now().UTC())
	} else {
		rule, err = s.firewall.Add(c.Request.Context(), srv.ID(), ip.String(), allocation.Port, request, time.Now().UTC())
	}
	if err != nil {
		s.setFirewallState("degraded")
		firewallRequestError(c, err)
		return
	}
	s.setFirewallState("healthy")
	status := http.StatusCreated
	if dryRun {
		status = http.StatusOK
	}
	c.JSON(status, gin.H{"rule": rule, "dry_run": dryRun})
}

func (s *sideroExtension) removeFirewallRule(c *gin.Context) {
	if !s.firewallEnabled(c) {
		return
	}
	if err := s.firewall.Remove(c.Request.Context(), middleware.ExtractServer(c).ID(), c.Param("rule")); err != nil {
		s.setFirewallState("degraded")
		firewallRequestError(c, err)
		return
	}
	s.setFirewallState("healthy")
	c.Status(http.StatusNoContent)
}

func firewallRequestError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, firewall.ErrUnsupported):
		unsupportedCapability(c)
	case errors.Is(err, firewall.ErrRuleLimit):
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeOperationLimitReached, "The firewall rule limit has been reached.", http.StatusTooManyRequests))
	case errors.Is(err, firewall.ErrRuleConflict):
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeOperationConflict, "The firewall rule conflicts with existing desired state.", http.StatusConflict))
	case errors.Is(err, firewall.ErrRuleNotFound):
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeFirewallRuleInvalid, "The firewall rule does not exist.", http.StatusNotFound))
	case errors.Is(err, firewall.ErrApplyFailed):
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeFirewallApplyFailed, "The firewall desired state could not be applied.", http.StatusServiceUnavailable).WithRetryable(true))
	default:
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeFirewallRuleInvalid, "The firewall rule is invalid.", http.StatusBadRequest))
	}
}

func (s *sideroExtension) sampleNetwork(ctx context.Context, servers *server.Manager, interval time.Duration, maximum int) {
	s.networkRunning.Store(true)
	defer s.networkRunning.Store(false)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, srv := range servers.All() {
				s.networkMu.Lock()
				buffer := s.network[srv.ID()]
				if buffer == nil {
					buffer = sideronetwork.NewBuffer(maximum)
					s.network[srv.ID()] = buffer
				}
				var sample sideronetwork.Sample
				if !srv.IsRunning() {
					sample = buffer.Unavailable(now)
				} else {
					proc := srv.Proc()
					sample = buffer.Add(sideronetwork.Counters{ReceiveBytes: proc.Network.RxBytes, TransmitBytes: proc.Network.TxBytes, ReceivePackets: proc.Network.RxPackets, TransmitPackets: proc.Network.TxPackets, ReceiveDrops: proc.Network.RxDropped, TransmitDrops: proc.Network.TxDropped}, now)
				}
				s.networkMu.Unlock()
				srv.Events().Publish("sidero network stats", sample)
			}
		}
	}
}

func (s *sideroExtension) getNetwork(c *gin.Context) {
	cfg := config.Get().Sidero
	if !cfg.Enabled || !cfg.NetworkStatistics.Enabled {
		featureDisabled(c)
		return
	}
	id := middleware.ExtractServer(c).ID()
	s.networkMu.RLock()
	buffer := s.network[id]
	s.networkMu.RUnlock()
	if buffer == nil {
		c.JSON(http.StatusOK, gin.H{"available": false, "samples": []any{}})
		return
	}
	latest, available := buffer.Latest()
	c.JSON(http.StatusOK, gin.H{"available": available, "current": latest, "samples": buffer.Recent()})
}

func (s *sideroExtension) gameQuery(c *gin.Context) {
	cfg := config.Get().Sidero
	if !cfg.Enabled || !cfg.GameQuery.Enabled {
		featureDisabled(c)
		return
	}
	srv := middleware.ExtractServer(c)
	provider := c.DefaultQuery("provider", "minecraft-java")
	if provider != "minecraft-java" && provider != "minecraft-bedrock" {
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeGameQueryUnsupported, "The requested game-query provider is unsupported.", http.StatusBadRequest))
		return
	}
	allocationID := int64(0)
	if value := c.Query("allocation_id"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 {
			invalidRequest(c)
			return
		}
		allocationID = parsed
	}
	host, port := srv.DefaultAllocation()
	if allocationID > 0 {
		found := false
		if allocation, ok := srv.SideroAllocation(allocationID); ok {
			if allocation.QueryProvider != "" && allocation.QueryProvider != provider {
				respondSideroError(c, sideroerrors.New(sideroerrors.CodeGameQueryUnsupported, "The allocation does not support this query provider.", http.StatusBadRequest))
				return
			}
			host, port, found = allocation.IP, allocation.Port, true
		}
		if !found {
			respondSideroError(c, sideroerrors.New(sideroerrors.CodeGameQueryUnsupported, "The requested allocation is not registered to this server.", http.StatusNotFound))
			return
		}
	}
	ip := net.ParseIP(host)
	if ip == nil || port < 1 || port > 65535 {
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeGameQueryUnsupported, "The server allocation cannot be queried safely.", http.StatusUnprocessableEntity))
		return
	}
	if ip.IsUnspecified() {
		if ip.To4() != nil {
			host = "127.0.0.1"
		} else {
			host = "::1"
		}
	}
	result, err := s.queries.Query(c.Request.Context(), srv.ID(), allocationID, provider, query.Target{Host: host, Port: port})
	if errors.Is(err, query.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeGameQueryTimeout, "The game query timed out.", http.StatusGatewayTimeout).WithRetryable(true))
		return
	}
	if err != nil {
		c.JSON(http.StatusOK, query.Result{Supported: true, Online: false, Provider: provider, Players: query.Players{Sample: []string{}}, QueriedAt: time.Now().UTC()})
		return
	}
	c.JSON(http.StatusOK, result)
}

type worldPathRequest struct {
	Path string `json:"path"`
}
type worldTransferRequest struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}
type worldImportRequest struct {
	Archive        string `json:"archive"`
	Destination    string `json:"destination"`
	ConflictPolicy string `json:"conflict_policy"`
}
type worldReplaceRequest struct {
	Source         string `json:"source"`
	Destination    string `json:"destination"`
	BackupExisting bool   `json:"backup_existing"`
}

func (s *sideroExtension) submitWorld(c *gin.Context, operationType string, runner operations.Runner) {
	cfg := config.Get().Sidero
	if !cfg.Enabled || !cfg.Worlds.Enabled {
		featureDisabled(c)
		return
	}
	op, err := s.operations.Submit(middleware.ExtractServer(c).ID(), operationType, c.GetHeader("Idempotency-Key"), runner)
	if err != nil {
		operationSubmissionError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, op)
}

func (s *sideroExtension) worldInspect(c *gin.Context) {
	var request worldPathRequest
	if c.ShouldBindJSON(&request) != nil {
		invalidRequest(c)
		return
	}
	if !clientPathsValid(request.Path) {
		invalidRequest(c)
		return
	}
	srv := middleware.ExtractServer(c)
	maximum := config.Get().Sidero.Worlds.MaximumCloneBytes
	s.submitWorld(c, "world_inspection", func(ctx context.Context, update operations.UpdateFunc) (any, *operations.SafeError) {
		result, err := worlds.Inspect(ctx, srv.Filesystem(), request.Path, maximum)
		if err != nil {
			return nil, safeOperationError(err)
		}
		return result, nil
	})
}

func (s *sideroExtension) worldSize(c *gin.Context) {
	var request worldPathRequest
	if c.ShouldBindJSON(&request) != nil {
		invalidRequest(c)
		return
	}
	if !clientPathsValid(request.Path) {
		invalidRequest(c)
		return
	}
	srv := middleware.ExtractServer(c)
	maximum := config.Get().Sidero.Worlds.MaximumCloneBytes
	s.submitWorld(c, "world_size", func(ctx context.Context, update operations.UpdateFunc) (any, *operations.SafeError) {
		result, err := worlds.Size(ctx, srv.Filesystem(), request.Path, maximum)
		if err != nil {
			return nil, safeOperationError(err)
		}
		return result, nil
	})
}

func (s *sideroExtension) worldClone(c *gin.Context) {
	var request worldTransferRequest
	if c.ShouldBindJSON(&request) != nil {
		invalidRequest(c)
		return
	}
	if !clientPathsValid(request.Source, request.Destination) {
		invalidRequest(c)
		return
	}
	srv := middleware.ExtractServer(c)
	maximum := config.Get().Sidero.Worlds.MaximumCloneBytes
	s.submitWorld(c, "world_clone", func(ctx context.Context, update operations.UpdateFunc) (any, *operations.SafeError) {
		result, err := worlds.Clone(ctx, srv.Filesystem(), request.Source, request.Destination, maximum, srv.IsRunning(), func(progress float64, message string) { update(progress, message) })
		if err != nil {
			return nil, safeOperationError(err)
		}
		return result, nil
	})
}

func (s *sideroExtension) worldImport(c *gin.Context) {
	var request worldImportRequest
	if c.ShouldBindJSON(&request) != nil {
		invalidRequest(c)
		return
	}
	if !clientPathsValid(request.Archive, request.Destination) {
		invalidRequest(c)
		return
	}
	srv := middleware.ExtractServer(c)
	cfg := config.Get().Sidero
	s.submitWorld(c, "world_import", func(ctx context.Context, update operations.UpdateFunc) (any, *operations.SafeError) {
		result, err := worlds.Import(ctx, srv.Filesystem(), request.Archive, request.Destination, request.ConflictPolicy, cfg.Worlds.MaximumImportBytes, srv.IsRunning(), archives.Limits{MaximumEntries: cfg.Archives.MaximumEntries, MaximumUncompressedBytes: min(cfg.Archives.MaximumUncompressedBytes, cfg.Worlds.MaximumImportBytes), MaximumSingleEntryBytes: cfg.Archives.MaximumSingleEntryBytes, MaximumCompressionRatio: cfg.Archives.MaximumCompressionRatio, AllowSymbolicLinks: false}, func(progress float64, message string) { update(progress, message) })
		if err != nil {
			return nil, safeOperationError(err)
		}
		return result, nil
	})
}

func (s *sideroExtension) worldArchive(c *gin.Context) {
	var request worldTransferRequest
	if c.ShouldBindJSON(&request) != nil {
		invalidRequest(c)
		return
	}
	if !clientPathsValid(request.Source, request.Destination) {
		invalidRequest(c)
		return
	}
	srv := middleware.ExtractServer(c)
	maximum := config.Get().Sidero.Worlds.MaximumCloneBytes
	s.submitWorld(c, "world_archive", func(ctx context.Context, update operations.UpdateFunc) (any, *operations.SafeError) {
		result, err := worlds.Archive(ctx, srv.Filesystem(), request.Source, request.Destination, maximum, func(progress float64, message string) { update(progress, message) })
		if err != nil {
			return nil, safeOperationError(err)
		}
		return result, nil
	})
}

func (s *sideroExtension) worldRename(c *gin.Context) {
	var request worldTransferRequest
	if c.ShouldBindJSON(&request) != nil {
		invalidRequest(c)
		return
	}
	if !clientPathsValid(request.Source, request.Destination) {
		invalidRequest(c)
		return
	}
	srv := middleware.ExtractServer(c)
	s.submitWorld(c, "world_rename", func(ctx context.Context, update operations.UpdateFunc) (any, *operations.SafeError) {
		if err := ctx.Err(); err != nil {
			return nil, safeOperationError(err)
		}
		result, err := worlds.Rename(srv.Filesystem(), request.Source, request.Destination, srv.IsRunning())
		if err != nil {
			return nil, safeOperationError(err)
		}
		return result, nil
	})
}

func (s *sideroExtension) worldReplace(c *gin.Context) {
	var request worldReplaceRequest
	if c.ShouldBindJSON(&request) != nil {
		invalidRequest(c)
		return
	}
	if !clientPathsValid(request.Source, request.Destination) {
		invalidRequest(c)
		return
	}
	srv := middleware.ExtractServer(c)
	s.submitWorld(c, "world_replace", func(ctx context.Context, update operations.UpdateFunc) (any, *operations.SafeError) {
		result, err := worlds.Replace(ctx, srv.Filesystem(), request.Source, request.Destination, request.BackupExisting, srv.IsRunning())
		if err != nil {
			return nil, safeOperationError(err)
		}
		return result, nil
	})
}

func (s *sideroExtension) health(c *gin.Context) {
	cfg := config.Get().Sidero
	state := "healthy"
	if !cfg.Enabled {
		state = "disabled"
	}
	if err := cfg.Validate(); err != nil {
		state = "misconfigured"
	}
	temporaryStorage := "disabled"
	if cfg.Enabled {
		temporaryStorage = checkTemporaryStorage()
		if temporaryStorage != "healthy" && state == "healthy" {
			state = "degraded"
		}
	}
	operationState := featureHealth(cfg.Enabled, s.operations != nil && s.cleanupRunning.Load())
	cleanupState := featureHealth(cfg.Enabled, s.cleanupRunning.Load())
	networkState := featureHealth(cfg.Enabled && cfg.NetworkStatistics.Enabled, s.networkRunning.Load())
	firewallState := "disabled"
	if cfg.Enabled && cfg.Firewall.Enabled {
		firewallState = s.getFirewallState()
		if firewallState != "healthy" && state == "healthy" {
			state = "degraded"
		}
	}
	if operationState == "unavailable" || cleanupState == "unavailable" {
		state = "unavailable"
	}
	c.JSON(http.StatusOK, gin.H{"state": state, "protocol_version": cfg.ProtocolVersion, "components": gin.H{
		"configuration":      map[bool]string{true: "healthy", false: "misconfigured"}[cfg.Validate() == nil],
		"operation_manager":  operationState,
		"cleanup_workers":    cleanupState,
		"temporary_storage":  temporaryStorage,
		"archive_support":    featureHealth(cfg.Enabled && cfg.Archives.InspectionEnabled, true),
		"upload_support":     featureHealth(cfg.Enabled && cfg.Uploads.ResumableEnabled, s.uploads != nil && temporaryStorage == "healthy"),
		"query_providers":    featureHealth(cfg.Enabled && cfg.GameQuery.Enabled, s.queries != nil),
		"installer_service":  featureHealth(cfg.Enabled && cfg.Installers.Enabled, temporaryStorage == "healthy"),
		"world_operations":   featureHealth(cfg.Enabled && cfg.Worlds.Enabled, cfg.Enabled && cfg.Worlds.Enabled),
		"network_statistics": networkState,
		"firewall":           firewallState,
		"firewall_cleanup":   featureHealth(cfg.Enabled && cfg.Firewall.Enabled, s.firewallCleanupRunning.Load()),
	}})
}

func featureHealth(enabled, available bool) string {
	if !enabled {
		return "disabled"
	}
	if available {
		return "healthy"
	}
	return "unavailable"
}

func checkTemporaryStorage() string {
	temporary, err := os.CreateTemp(config.Get().System.TmpDirectory, ".sidero-health-*")
	if err != nil {
		return "unavailable"
	}
	name := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(name)
		return "unavailable"
	}
	if err := os.Remove(name); err != nil {
		return "degraded"
	}
	return "healthy"
}
func unsupportedCapability(c *gin.Context) {
	respondSideroError(c, sideroerrors.New(sideroerrors.CodeUnsupportedCapability, "This capability is not supported by this Wings build.", http.StatusNotImplemented))
}

func (s *sideroExtension) listOperations(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"operations": s.operations.List(middleware.ExtractServer(c).ID())})
}
func (s *sideroExtension) getOperation(c *gin.Context) {
	op, ok := s.operations.Get(middleware.ExtractServer(c).ID(), c.Param("operation"))
	if !ok {
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeOperationNotFound, "The requested operation does not exist.", http.StatusNotFound))
		return
	}
	c.JSON(http.StatusOK, op)
}
func (s *sideroExtension) cancelOperation(c *gin.Context) {
	if !s.operations.Cancel(middleware.ExtractServer(c).ID(), c.Param("operation")) {
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeOperationNotFound, "The requested operation does not exist or is already complete.", http.StatusNotFound))
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *sideroExtension) search(c *gin.Context) {
	cfg := config.Get().Sidero
	if !cfg.Enabled || !cfg.Files.SearchEnabled {
		featureDisabled(c)
		return
	}
	var request files.SearchRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		invalidRequest(c)
		return
	}
	if request.MaximumDepth == 0 || request.MaximumDepth > cfg.Files.MaximumSearchDepth {
		request.MaximumDepth = cfg.Files.MaximumSearchDepth
	}
	if request.MaximumResults == 0 || request.MaximumResults > cfg.Files.MaximumSearchResults {
		request.MaximumResults = cfg.Files.MaximumSearchResults
	}
	if request.MaximumEntries == 0 || request.MaximumEntries > cfg.Files.MaximumSearchEntries {
		request.MaximumEntries = cfg.Files.MaximumSearchEntries
	}
	server := middleware.ExtractServer(c)
	op, err := s.operations.Submit(server.ID(), "file_search", c.GetHeader("Idempotency-Key"), func(ctx context.Context, update operations.UpdateFunc) (any, *operations.SafeError) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		update(0.05, "Validating search.")
		result, err := files.Search(ctx, server.Filesystem(), request)
		if err != nil {
			return nil, safeOperationError(err)
		}
		return result, nil
	})
	if err != nil {
		operationSubmissionError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, op)
}

func (s *sideroExtension) contentSearch(c *gin.Context) {
	cfg := config.Get().Sidero
	if !cfg.Enabled || !cfg.Files.ContentSearchEnabled {
		featureDisabled(c)
		return
	}
	var request files.ContentSearchRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		invalidRequest(c)
		return
	}
	if request.MaximumFileSize == 0 || request.MaximumFileSize > cfg.Files.MaximumContentFileSize {
		request.MaximumFileSize = cfg.Files.MaximumContentFileSize
	}
	if request.MaximumTotalMatches == 0 || request.MaximumTotalMatches > cfg.Files.MaximumContentMatches {
		request.MaximumTotalMatches = cfg.Files.MaximumContentMatches
	}
	if request.MaximumMatchesPerFile == 0 || request.MaximumMatchesPerFile > request.MaximumTotalMatches {
		request.MaximumMatchesPerFile = request.MaximumTotalMatches
	}
	if request.MaximumExcerptBytes == 0 || request.MaximumExcerptBytes > cfg.Files.MaximumExcerptBytes {
		request.MaximumExcerptBytes = cfg.Files.MaximumExcerptBytes
	}
	if request.MaximumDepth == 0 || request.MaximumDepth > cfg.Files.MaximumSearchDepth {
		request.MaximumDepth = cfg.Files.MaximumSearchDepth
	}
	if request.MaximumFilesScanned == 0 || request.MaximumFilesScanned > cfg.Files.MaximumContentFiles {
		request.MaximumFilesScanned = cfg.Files.MaximumContentFiles
	}
	server := middleware.ExtractServer(c)
	op, err := s.operations.Submit(server.ID(), "content_search", c.GetHeader("Idempotency-Key"), func(ctx context.Context, update operations.UpdateFunc) (any, *operations.SafeError) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		update(0.05, "Searching file contents.")
		result, err := files.ContentSearch(ctx, server.Filesystem(), request)
		if err != nil {
			return nil, safeOperationError(err)
		}
		return result, nil
	})
	if err != nil {
		operationSubmissionError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, op)
}

func (s *sideroExtension) remoteDownload(c *gin.Context) {
	cfg := config.Get().Sidero
	if !cfg.Enabled || !cfg.RemoteDownload.Enabled {
		featureDisabled(c)
		return
	}
	var request download.Request
	if err := c.ShouldBindJSON(&request); err != nil {
		invalidRequest(c)
		return
	}
	if !clientPathsValid(request.Destination) {
		invalidRequest(c)
		return
	}
	server := middleware.ExtractServer(c)
	client := download.NewClient(download.Policy{MaximumBytes: cfg.RemoteDownload.MaximumBytes, MaximumRedirects: cfg.RemoteDownload.MaximumRedirects, ConnectTimeout: time.Duration(cfg.RemoteDownload.ConnectionTimeoutSeconds) * time.Second, TotalTimeout: time.Duration(cfg.RemoteDownload.TotalTimeoutSeconds) * time.Second, AllowPrivateNetworks: cfg.RemoteDownload.AllowPrivateNetworks, AllowedHosts: cfg.RemoteDownload.AllowedHosts, BlockedHosts: cfg.RemoteDownload.BlockedHosts})
	op, err := s.operations.Submit(server.ID(), "remote_download", c.GetHeader("Idempotency-Key"), func(ctx context.Context, update operations.UpdateFunc) (any, *operations.SafeError) {
		result, err := download.Execute(ctx, server.Filesystem(), client, request, func(progress float64, message string) { update(progress, message) })
		if err != nil {
			return nil, safeOperationError(err)
		}
		return result, nil
	})
	if err != nil {
		operationSubmissionError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, op)
}

func (s *sideroExtension) executeInstaller(c *gin.Context) {
	cfg := config.Get().Sidero
	if !cfg.Enabled || !cfg.Installers.Enabled {
		featureDisabled(c)
		return
	}
	var request installers.Request
	if err := c.ShouldBindJSON(&request); err != nil {
		invalidRequest(c)
		return
	}
	request.BackupExisting = request.BackupExisting || cfg.Installers.BackupExisting
	server := middleware.ExtractServer(c)
	client := download.NewClient(download.Policy{MaximumBytes: cfg.Installers.MaximumDownloadBytes, MaximumRedirects: cfg.RemoteDownload.MaximumRedirects, ConnectTimeout: time.Duration(cfg.RemoteDownload.ConnectionTimeoutSeconds) * time.Second, TotalTimeout: time.Duration(cfg.RemoteDownload.TotalTimeoutSeconds) * time.Second, AllowPrivateNetworks: cfg.RemoteDownload.AllowPrivateNetworks, AllowedHosts: cfg.RemoteDownload.AllowedHosts, BlockedHosts: cfg.RemoteDownload.BlockedHosts})
	op, err := s.operations.Submit(server.ID(), "installer_execution", request.OperationKey, func(ctx context.Context, update operations.UpdateFunc) (any, *operations.SafeError) {
		result, err := installers.Execute(ctx, server.Filesystem(), client, request, installers.Limits{MaximumFiles: cfg.Installers.MaximumFilesPerOperation, MaximumDownloadBytes: cfg.Installers.MaximumDownloadBytes}, func(progress float64, message string) { update(progress, message) })
		if err != nil {
			return nil, safeOperationError(err)
		}
		return result, nil
	})
	if err != nil {
		operationSubmissionError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, op)
}

func (s *sideroExtension) installModpack(c *gin.Context) {
	cfg := config.Get().Sidero
	if !cfg.Enabled || !cfg.Installers.Enabled {
		featureDisabled(c)
		return
	}
	var request installers.ModpackRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		invalidRequest(c)
		return
	}
	request.BackupExisting = request.BackupExisting || cfg.Installers.BackupExisting
	server := middleware.ExtractServer(c)
	client := download.NewClient(download.Policy{MaximumBytes: cfg.Installers.MaximumDownloadBytes, MaximumRedirects: cfg.RemoteDownload.MaximumRedirects, ConnectTimeout: time.Duration(cfg.RemoteDownload.ConnectionTimeoutSeconds) * time.Second, TotalTimeout: time.Duration(cfg.RemoteDownload.TotalTimeoutSeconds) * time.Second, AllowPrivateNetworks: cfg.RemoteDownload.AllowPrivateNetworks, AllowedHosts: cfg.RemoteDownload.AllowedHosts, BlockedHosts: cfg.RemoteDownload.BlockedHosts})
	op, err := s.operations.Submit(server.ID(), "modpack_installation", request.OperationKey, func(ctx context.Context, update operations.UpdateFunc) (any, *operations.SafeError) {
		result, err := installers.InstallModpack(ctx, server.Filesystem(), client, request, installers.Limits{MaximumFiles: cfg.Installers.MaximumFilesPerOperation, MaximumDownloadBytes: cfg.Installers.MaximumDownloadBytes}, archives.Limits{MaximumEntries: cfg.Archives.MaximumEntries, MaximumUncompressedBytes: cfg.Archives.MaximumUncompressedBytes, MaximumSingleEntryBytes: cfg.Archives.MaximumSingleEntryBytes, MaximumCompressionRatio: cfg.Archives.MaximumCompressionRatio, AllowSymbolicLinks: false}, func(progress float64, message string) { update(progress, message) })
		if err != nil {
			return nil, safeOperationError(err)
		}
		return result, nil
	})
	if err != nil {
		operationSubmissionError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, op)
}

type archiveInspectionRequest struct {
	Path string `json:"path"`
}

func (s *sideroExtension) inspectArchive(c *gin.Context) {
	cfg := config.Get().Sidero
	if !cfg.Enabled || !cfg.Archives.InspectionEnabled {
		featureDisabled(c)
		return
	}
	var request archiveInspectionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		invalidRequest(c)
		return
	}
	if !clientPathsValid(request.Path) {
		invalidRequest(c)
		return
	}
	server := middleware.ExtractServer(c)
	op, err := s.operations.Submit(server.ID(), "archive_inspection", c.GetHeader("Idempotency-Key"), func(ctx context.Context, update operations.UpdateFunc) (any, *operations.SafeError) {
		update(0.05, "Inspecting archive.")
		result, err := archives.Inspect(ctx, server.Filesystem(), request.Path, archives.Limits{MaximumEntries: cfg.Archives.MaximumEntries, MaximumUncompressedBytes: cfg.Archives.MaximumUncompressedBytes, MaximumSingleEntryBytes: cfg.Archives.MaximumSingleEntryBytes, MaximumCompressionRatio: cfg.Archives.MaximumCompressionRatio, AllowSymbolicLinks: cfg.Archives.AllowSymbolicLinks})
		if err != nil {
			return nil, safeOperationError(err)
		}
		return result, nil
	})
	if err != nil {
		operationSubmissionError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, op)
}

func (s *sideroExtension) extractArchive(c *gin.Context) {
	cfg := config.Get().Sidero
	if !cfg.Enabled || !cfg.Archives.InspectionEnabled {
		featureDisabled(c)
		return
	}
	var request archives.ExtractRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		invalidRequest(c)
		return
	}
	if !clientPathsValid(request.Archive, request.Destination) {
		invalidRequest(c)
		return
	}
	server := middleware.ExtractServer(c)
	op, err := s.operations.Submit(server.ID(), "archive_extraction", c.GetHeader("Idempotency-Key"), func(ctx context.Context, update operations.UpdateFunc) (any, *operations.SafeError) {
		result, err := archives.Extract(ctx, server.Filesystem(), request, archives.Limits{MaximumEntries: cfg.Archives.MaximumEntries, MaximumUncompressedBytes: cfg.Archives.MaximumUncompressedBytes, MaximumSingleEntryBytes: cfg.Archives.MaximumSingleEntryBytes, MaximumCompressionRatio: cfg.Archives.MaximumCompressionRatio, AllowSymbolicLinks: false}, func(progress float64, message string) { update(progress, message) })
		if err != nil {
			return nil, safeOperationError(err)
		}
		return result, nil
	})
	if err != nil {
		operationSubmissionError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, op)
}

func (s *sideroExtension) probe(c *gin.Context) {
	cfg := config.Get().Sidero
	if !cfg.Enabled {
		featureDisabled(c)
		return
	}
	var request files.ProbeRequest
	if c.ShouldBindJSON(&request) != nil {
		invalidRequest(c)
		return
	}
	result, err := files.Probe(c.Request.Context(), middleware.ExtractServer(c).Filesystem(), request, cfg.Files.MaximumProbePaths, cfg.Files.MaximumContentFileSize)
	if err != nil {
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeInvalidPath, "One or more probe paths are invalid.", http.StatusBadRequest))
		return
	}
	c.JSON(http.StatusOK, gin.H{"files": result})
}

func (s *sideroExtension) conditionalWrite(c *gin.Context) {
	cfg := config.Get().Sidero
	if !cfg.Enabled {
		featureDisabled(c)
		return
	}
	var request files.ConditionalWriteRequest
	if c.ShouldBindJSON(&request) != nil {
		invalidRequest(c)
		return
	}
	srv := middleware.ExtractServer(c)
	result, err := files.ConditionalWrite(c.Request.Context(), srv.Filesystem(), request, cfg.Files.MaximumWriteBytes)
	if errors.Is(err, files.ErrConditionalConflict) {
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeConditionalWriteConflict, "The file changed since it was last read.", http.StatusConflict))
		return
	}
	if err != nil {
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeInvalidPath, "The file could not be written safely.", http.StatusBadRequest))
		return
	}
	srv.Log().Info("sidero conditional configuration write completed")
	srv.SaveActivity(srv.NewRequestActivity("", c.ClientIP()), models.Event("server:sidero.file.conditional-write"), models.ActivityMeta{"path": request.Path, "backup_created": result.Backup != ""})
	c.JSON(http.StatusOK, result)
}

func (s *sideroExtension) createUpload(c *gin.Context) {
	cfg := config.Get().Sidero
	if !cfg.Enabled || !cfg.Uploads.ResumableEnabled {
		featureDisabled(c)
		return
	}
	var request uploads.CreateRequest
	if c.ShouldBindJSON(&request) != nil {
		invalidRequest(c)
		return
	}
	session, err := s.uploads.Create(middleware.ExtractServer(c).ID(), middleware.ExtractServer(c).Filesystem(), request)
	if err != nil {
		respondUploadError(c, err)
		return
	}
	c.JSON(http.StatusCreated, session)
}
func (s *sideroExtension) getUpload(c *gin.Context) {
	session, err := s.uploads.Get(middleware.ExtractServer(c).ID(), c.Param("upload"))
	if err != nil {
		respondUploadError(c, err)
		return
	}
	c.JSON(http.StatusOK, session)
}
func (s *sideroExtension) putUploadChunk(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		invalidRequest(c)
		return
	}
	err = s.uploads.PutChunk(c.Request.Context(), middleware.ExtractServer(c).ID(), c.Param("upload"), index, c.Request.Body, c.Request.ContentLength)
	if err != nil {
		respondUploadError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
func (s *sideroExtension) completeUpload(c *gin.Context) {
	result, err := s.uploads.Complete(c.Request.Context(), middleware.ExtractServer(c).ID(), c.Param("upload"))
	if err != nil {
		respondUploadError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
func (s *sideroExtension) cancelUpload(c *gin.Context) {
	if err := s.uploads.Cancel(middleware.ExtractServer(c).ID(), c.Param("upload")); err != nil {
		respondUploadError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func respondUploadError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, uploads.ErrNotFound), errors.Is(err, uploads.ErrExpired):
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeUploadSessionExpired, "The upload session does not exist or has expired.", http.StatusNotFound))
	case errors.Is(err, uploads.ErrIncomplete):
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeUploadIncomplete, "The upload is incomplete.", http.StatusConflict))
	case errors.Is(err, uploads.ErrChecksum):
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeUploadChecksumMismatch, "The upload checksum did not match.", http.StatusUnprocessableEntity))
	case errors.Is(err, uploads.ErrLimit):
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeOperationLimitReached, "The upload session limit has been reached.", http.StatusTooManyRequests).WithRetryable(true))
	case errors.Is(err, uploads.ErrConflict):
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeOperationConflict, "The upload conflicts with an existing file or request.", http.StatusConflict))
	default:
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeUploadChunkInvalid, "The upload request is invalid.", http.StatusBadRequest))
	}
}

func respondSideroError(c *gin.Context, err *sideroerrors.Error) {
	c.AbortWithStatusJSON(err.HTTPStatus, err)
}
func featureDisabled(c *gin.Context) {
	respondSideroError(c, sideroerrors.New(sideroerrors.CodeFeatureDisabled, "This Sidero capability is disabled on this node.", http.StatusNotImplemented))
}
func invalidRequest(c *gin.Context) {
	respondSideroError(c, sideroerrors.New(sideroerrors.CodeInvalidPath, "The request contains invalid or unsafe data.", http.StatusBadRequest))
}
func clientPathsValid(paths ...string) bool {
	for _, value := range paths {
		if _, err := files.NormalizeClientPath(value); err != nil {
			return false
		}
	}
	return true
}
func operationSubmissionError(c *gin.Context, err error) {
	if errors.Is(err, operations.ErrInvalidSubmission) {
		invalidRequest(c)
		return
	}
	if errors.Is(err, operations.ErrLimitReached) {
		respondSideroError(c, sideroerrors.New(sideroerrors.CodeOperationLimitReached, "The node operation limit has been reached.", http.StatusTooManyRequests).WithRetryable(true))
		return
	}
	respondSideroError(c, sideroerrors.New(sideroerrors.CodeInternal, "The operation could not be created.", http.StatusInternalServerError))
}

func safeOperationError(err error) *operations.SafeError {
	switch {
	case errors.Is(err, context.Canceled):
		return &operations.SafeError{Code: string(sideroerrors.CodeOperationCancelled), Message: "The operation was cancelled."}
	case errors.Is(err, context.DeadlineExceeded):
		return &operations.SafeError{Code: string(sideroerrors.CodeOperationTimedOut), Message: "The operation timed out.", Retryable: true}
	case errors.Is(err, files.ErrInvalidPath):
		return &operations.SafeError{Code: string(sideroerrors.CodeInvalidPath), Message: "The path is invalid or outside the server root."}
	case errors.Is(err, files.ErrLimitReached):
		return &operations.SafeError{Code: string(sideroerrors.CodeSearchLimitReached), Message: "The configured file-operation limit was reached."}
	case errors.Is(err, download.ErrHostDenied):
		return &operations.SafeError{Code: string(sideroerrors.CodeRemoteHostDenied), Message: "The remote host is not permitted."}
	case errors.Is(err, download.ErrRedirectDenied):
		return &operations.SafeError{Code: string(sideroerrors.CodeRemoteRedirectDenied), Message: "The remote redirect is not permitted."}
	case errors.Is(err, download.ErrTimeout):
		return &operations.SafeError{Code: string(sideroerrors.CodeOperationTimedOut), Message: "The remote download timed out.", Retryable: true}
	case errors.Is(err, download.ErrSizeExceeded):
		return &operations.SafeError{Code: string(sideroerrors.CodeRemoteSizeExceeded), Message: "The remote file exceeds the permitted size."}
	case errors.Is(err, download.ErrChecksumMismatch):
		return &operations.SafeError{Code: string(sideroerrors.CodeRemoteChecksumMismatch), Message: "The downloaded file checksum did not match."}
	case errors.Is(err, download.ErrConflict):
		return &operations.SafeError{Code: string(sideroerrors.CodeOperationConflict), Message: "The destination already exists."}
	case errors.Is(err, archives.ErrMalformed):
		return &operations.SafeError{Code: string(sideroerrors.CodeArchiveMalformed), Message: "The archive is malformed or unsupported."}
	case errors.Is(err, archives.ErrTraversal):
		return &operations.SafeError{Code: string(sideroerrors.CodeArchiveTraversalDetected), Message: "The archive contains an unsafe path."}
	case errors.Is(err, archives.ErrUnsafeEntry):
		return &operations.SafeError{Code: string(sideroerrors.CodeUnsafeSymbolicLink), Message: "The archive contains an unsafe entry type."}
	case errors.Is(err, archives.ErrEntryLimit):
		return &operations.SafeError{Code: string(sideroerrors.CodeArchiveEntryLimitExceeded), Message: "The archive contains too many entries."}
	case errors.Is(err, archives.ErrSizeLimit):
		return &operations.SafeError{Code: string(sideroerrors.CodeArchiveSizeLimitExceeded), Message: "The archive exceeds the extraction size limit."}
	case errors.Is(err, archives.ErrRatioLimit):
		return &operations.SafeError{Code: string(sideroerrors.CodeArchiveRatioLimitExceeded), Message: "The archive compression ratio exceeds the permitted limit."}
	case errors.Is(err, archives.ErrConflict):
		return &operations.SafeError{Code: string(sideroerrors.CodeOperationConflict), Message: "The archive destination conflicts with an existing path."}
	case errors.Is(err, installers.ErrManifestInvalid):
		return &operations.SafeError{Code: string(sideroerrors.CodeInstallerManifestInvalid), Message: "The resolved installer manifest is invalid."}
	case errors.Is(err, installers.ErrDestinationInvalid):
		return &operations.SafeError{Code: string(sideroerrors.CodeInstallerDestinationInvalid), Message: "An installer destination is invalid."}
	case errors.Is(err, installers.ErrDownloadLimit):
		return &operations.SafeError{Code: string(sideroerrors.CodeRemoteSizeExceeded), Message: "The installer exceeds the permitted download size."}
	case errors.Is(err, installers.ErrConflict):
		return &operations.SafeError{Code: string(sideroerrors.CodeOperationConflict), Message: "An installer destination conflicts with an existing path."}
	case errors.Is(err, worlds.ErrInvalidWorld):
		return &operations.SafeError{Code: string(sideroerrors.CodeWorldInvalid), Message: "The selected path is not a recognized Minecraft world."}
	case errors.Is(err, worlds.ErrServerRunning):
		return &operations.SafeError{Code: string(sideroerrors.CodeOperationConflict), Message: "The server must be offline for this world operation."}
	case errors.Is(err, worlds.ErrSizeLimit):
		return &operations.SafeError{Code: string(sideroerrors.CodeArchiveSizeLimitExceeded), Message: "The world exceeds the permitted size."}
	case errors.Is(err, worlds.ErrConflict):
		return &operations.SafeError{Code: string(sideroerrors.CodeOperationConflict), Message: "The world operation conflicts with an existing path."}
	default:
		return &operations.SafeError{Code: string(sideroerrors.CodeInternal), Message: "The operation failed on the node."}
	}
}
