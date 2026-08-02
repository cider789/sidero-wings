package config

import (
	"fmt"
	"net/netip"
)

// SideroConfiguration contains only node-side execution policy. Product metadata,
// users, permissions, billing, and long-term history remain Panel responsibilities.
type SideroConfiguration struct {
	Enabled         bool `default:"true" yaml:"enabled" json:"enabled"`
	ProtocolVersion int  `default:"1" yaml:"protocol_version" json:"protocol_version"`

	Operations struct {
		MaximumConcurrentGlobal    int `default:"16" yaml:"maximum_concurrent_global" json:"maximum_concurrent_global"`
		MaximumConcurrentPerServer int `default:"3" yaml:"maximum_concurrent_per_server" json:"maximum_concurrent_per_server"`
		RetentionSeconds           int `default:"3600" yaml:"retention_seconds" json:"retention_seconds"`
		CleanupIntervalSeconds     int `default:"300" yaml:"cleanup_interval_seconds" json:"cleanup_interval_seconds"`
	} `yaml:"operations" json:"operations"`

	Files struct {
		SearchEnabled          bool  `default:"true" yaml:"search_enabled" json:"search_enabled"`
		ContentSearchEnabled   bool  `default:"true" yaml:"content_search_enabled" json:"content_search_enabled"`
		MaximumSearchResults   int   `default:"1000" yaml:"maximum_search_results" json:"maximum_search_results"`
		MaximumSearchDepth     int   `default:"64" yaml:"maximum_search_depth" json:"maximum_search_depth"`
		MaximumSearchEntries   int   `default:"100000" yaml:"maximum_search_entries" json:"maximum_search_entries"`
		MaximumContentFileSize int64 `default:"10485760" yaml:"maximum_content_file_size" json:"maximum_content_file_size"`
		MaximumContentFiles    int   `default:"10000" yaml:"maximum_content_files" json:"maximum_content_files"`
		MaximumContentMatches  int   `default:"1000" yaml:"maximum_content_matches" json:"maximum_content_matches"`
		MaximumExcerptBytes    int   `default:"512" yaml:"maximum_excerpt_bytes" json:"maximum_excerpt_bytes"`
		MaximumProbePaths      int   `default:"100" yaml:"maximum_probe_paths" json:"maximum_probe_paths"`
		MaximumWriteBytes      int64 `default:"1048576" yaml:"maximum_write_bytes" json:"maximum_write_bytes"`
	} `yaml:"files" json:"files"`

	RemoteDownload struct {
		Enabled                  bool     `default:"true" yaml:"enabled" json:"enabled"`
		MaximumBytes             int64    `default:"1073741824" yaml:"maximum_bytes" json:"maximum_bytes"`
		MaximumRedirects         int      `default:"5" yaml:"maximum_redirects" json:"maximum_redirects"`
		ConnectionTimeoutSeconds int      `default:"10" yaml:"connection_timeout_seconds" json:"connection_timeout_seconds"`
		TotalTimeoutSeconds      int      `default:"900" yaml:"total_timeout_seconds" json:"total_timeout_seconds"`
		AllowPrivateNetworks     bool     `default:"false" yaml:"allow_private_networks" json:"allow_private_networks"`
		AllowedHosts             []string `yaml:"allowed_hosts" json:"allowed_hosts"`
		BlockedHosts             []string `yaml:"blocked_hosts" json:"blocked_hosts"`
	} `yaml:"remote_download" json:"remote_download"`

	Archives struct {
		InspectionEnabled        bool    `default:"true" yaml:"inspection_enabled" json:"inspection_enabled"`
		MaximumEntries           int     `default:"50000" yaml:"maximum_entries" json:"maximum_entries"`
		MaximumUncompressedBytes int64   `default:"10737418240" yaml:"maximum_uncompressed_bytes" json:"maximum_uncompressed_bytes"`
		MaximumSingleEntryBytes  int64   `default:"2147483648" yaml:"maximum_single_entry_bytes" json:"maximum_single_entry_bytes"`
		MaximumCompressionRatio  float64 `default:"200" yaml:"maximum_compression_ratio" json:"maximum_compression_ratio"`
		AllowSymbolicLinks       bool    `default:"false" yaml:"allow_symbolic_links" json:"allow_symbolic_links"`
	} `yaml:"archives" json:"archives"`

	Uploads struct {
		ResumableEnabled           bool  `default:"true" yaml:"resumable_enabled" json:"resumable_enabled"`
		ChunkSize                  int64 `default:"8388608" yaml:"chunk_size" json:"chunk_size"`
		SessionExpirySeconds       int   `default:"86400" yaml:"session_expiry_seconds" json:"session_expiry_seconds"`
		MaximumConcurrentPerServer int   `default:"3" yaml:"maximum_concurrent_per_server" json:"maximum_concurrent_per_server"`
		MaximumUploadBytes         int64 `default:"10737418240" yaml:"maximum_upload_bytes" json:"maximum_upload_bytes"`
	} `yaml:"uploads" json:"uploads"`

	Installers struct {
		Enabled                  bool  `default:"true" yaml:"enabled" json:"enabled"`
		MaximumDownloadBytes     int64 `default:"5368709120" yaml:"maximum_download_bytes" json:"maximum_download_bytes"`
		MaximumFilesPerOperation int   `default:"10000" yaml:"maximum_files_per_operation" json:"maximum_files_per_operation"`
		BackupExisting           bool  `default:"true" yaml:"backup_existing" json:"backup_existing"`
	} `yaml:"installers" json:"installers"`

	GameQuery struct {
		Enabled               bool `default:"true" yaml:"enabled" json:"enabled"`
		TimeoutMilliseconds   int  `default:"3000" yaml:"timeout_milliseconds" json:"timeout_milliseconds"`
		CacheSeconds          int  `default:"15" yaml:"cache_seconds" json:"cache_seconds"`
		StaleSeconds          int  `default:"60" yaml:"stale_seconds" json:"stale_seconds"`
		MaximumRetries        int  `default:"1" yaml:"maximum_retries" json:"maximum_retries"`
		OfflineBackoffSeconds int  `default:"60" yaml:"offline_backoff_seconds" json:"offline_backoff_seconds"`
	} `yaml:"game_query" json:"game_query"`

	Worlds struct {
		Enabled            bool  `default:"true" yaml:"enabled" json:"enabled"`
		MaximumImportBytes int64 `default:"10737418240" yaml:"maximum_import_bytes" json:"maximum_import_bytes"`
		MaximumCloneBytes  int64 `default:"10737418240" yaml:"maximum_clone_bytes" json:"maximum_clone_bytes"`
	} `yaml:"worlds" json:"worlds"`

	NetworkStatistics struct {
		Enabled               bool `default:"true" yaml:"enabled" json:"enabled"`
		SampleIntervalSeconds int  `default:"5" yaml:"sample_interval_seconds" json:"sample_interval_seconds"`
		RecentSampleCount     int  `default:"120" yaml:"recent_sample_count" json:"recent_sample_count"`
	} `yaml:"network_statistics" json:"network_statistics"`

	RateLimits struct {
		WindowSeconds             int `default:"60" yaml:"window_seconds" json:"window_seconds"`
		MaximumGlobalPerRoute     int `default:"240" yaml:"maximum_global_per_route" json:"maximum_global_per_route"`
		MaximumPerServerPerRoute  int `default:"30" yaml:"maximum_per_server_per_route" json:"maximum_per_server_per_route"`
		MaximumTrackedServerRoute int `default:"10000" yaml:"maximum_tracked_server_route" json:"maximum_tracked_server_route"`
	} `yaml:"rate_limits" json:"rate_limits"`

	Firewall struct {
		Enabled                     bool     `default:"false" yaml:"enabled" json:"enabled"`
		MaximumRulesPerServer       int      `default:"50" yaml:"maximum_rules_per_server" json:"maximum_rules_per_server"`
		TemporaryRuleCleanupSeconds int      `default:"60" yaml:"temporary_rule_cleanup_seconds" json:"temporary_rule_cleanup_seconds"`
		AllowedSources              []string `yaml:"allowed_sources" json:"allowed_sources"`
		BlockedSources              []string `yaml:"blocked_sources" json:"blocked_sources"`
	} `yaml:"firewall" json:"firewall"`
}

// Validate returns a controlled configuration error and never panics. Bounds are
// intentionally conservative so a typo cannot create unbounded node work.
func (c SideroConfiguration) Validate() error {
	positive := []struct {
		name  string
		value int64
	}{
		{"protocol_version", int64(c.ProtocolVersion)},
		{"operations.maximum_concurrent_global", int64(c.Operations.MaximumConcurrentGlobal)},
		{"operations.maximum_concurrent_per_server", int64(c.Operations.MaximumConcurrentPerServer)},
		{"operations.retention_seconds", int64(c.Operations.RetentionSeconds)},
		{"operations.cleanup_interval_seconds", int64(c.Operations.CleanupIntervalSeconds)},
		{"files.maximum_search_results", int64(c.Files.MaximumSearchResults)},
		{"files.maximum_search_depth", int64(c.Files.MaximumSearchDepth)},
		{"files.maximum_search_entries", int64(c.Files.MaximumSearchEntries)},
		{"files.maximum_content_file_size", c.Files.MaximumContentFileSize},
		{"files.maximum_content_files", int64(c.Files.MaximumContentFiles)},
		{"files.maximum_content_matches", int64(c.Files.MaximumContentMatches)},
		{"files.maximum_excerpt_bytes", int64(c.Files.MaximumExcerptBytes)},
		{"files.maximum_probe_paths", int64(c.Files.MaximumProbePaths)},
		{"files.maximum_write_bytes", c.Files.MaximumWriteBytes},
		{"remote_download.maximum_bytes", c.RemoteDownload.MaximumBytes},
		{"remote_download.connection_timeout_seconds", int64(c.RemoteDownload.ConnectionTimeoutSeconds)},
		{"remote_download.total_timeout_seconds", int64(c.RemoteDownload.TotalTimeoutSeconds)},
		{"archives.maximum_entries", int64(c.Archives.MaximumEntries)},
		{"archives.maximum_uncompressed_bytes", c.Archives.MaximumUncompressedBytes},
		{"archives.maximum_single_entry_bytes", c.Archives.MaximumSingleEntryBytes},
		{"uploads.chunk_size", c.Uploads.ChunkSize},
		{"uploads.session_expiry_seconds", int64(c.Uploads.SessionExpirySeconds)},
		{"uploads.maximum_concurrent_per_server", int64(c.Uploads.MaximumConcurrentPerServer)},
		{"uploads.maximum_upload_bytes", c.Uploads.MaximumUploadBytes},
		{"installers.maximum_download_bytes", c.Installers.MaximumDownloadBytes},
		{"installers.maximum_files_per_operation", int64(c.Installers.MaximumFilesPerOperation)},
		{"game_query.timeout_milliseconds", int64(c.GameQuery.TimeoutMilliseconds)},
		{"game_query.cache_seconds", int64(c.GameQuery.CacheSeconds)},
		{"game_query.stale_seconds", int64(c.GameQuery.StaleSeconds)},
		{"game_query.offline_backoff_seconds", int64(c.GameQuery.OfflineBackoffSeconds)},
		{"worlds.maximum_import_bytes", c.Worlds.MaximumImportBytes},
		{"worlds.maximum_clone_bytes", c.Worlds.MaximumCloneBytes},
		{"network_statistics.sample_interval_seconds", int64(c.NetworkStatistics.SampleIntervalSeconds)},
		{"network_statistics.recent_sample_count", int64(c.NetworkStatistics.RecentSampleCount)},
		{"rate_limits.window_seconds", int64(c.RateLimits.WindowSeconds)},
		{"rate_limits.maximum_global_per_route", int64(c.RateLimits.MaximumGlobalPerRoute)},
		{"rate_limits.maximum_per_server_per_route", int64(c.RateLimits.MaximumPerServerPerRoute)},
		{"rate_limits.maximum_tracked_server_route", int64(c.RateLimits.MaximumTrackedServerRoute)},
		{"firewall.maximum_rules_per_server", int64(c.Firewall.MaximumRulesPerServer)},
		{"firewall.temporary_rule_cleanup_seconds", int64(c.Firewall.TemporaryRuleCleanupSeconds)},
	}
	for _, v := range positive {
		if v.value <= 0 {
			return fmt.Errorf("sidero: %s must be greater than zero", v.name)
		}
	}
	if c.Operations.MaximumConcurrentPerServer > c.Operations.MaximumConcurrentGlobal {
		return fmt.Errorf("sidero: operations.maximum_concurrent_per_server cannot exceed maximum_concurrent_global")
	}
	if c.RemoteDownload.MaximumRedirects < 0 || c.RemoteDownload.MaximumRedirects > 20 {
		return fmt.Errorf("sidero: remote_download.maximum_redirects must be between 0 and 20")
	}
	if c.Archives.MaximumCompressionRatio < 1 {
		return fmt.Errorf("sidero: archives.maximum_compression_ratio must be at least 1")
	}
	if c.Archives.MaximumSingleEntryBytes > c.Archives.MaximumUncompressedBytes {
		return fmt.Errorf("sidero: archives.maximum_single_entry_bytes cannot exceed maximum_uncompressed_bytes")
	}
	if c.Uploads.ChunkSize > c.Uploads.MaximumUploadBytes {
		return fmt.Errorf("sidero: uploads.chunk_size cannot exceed maximum_upload_bytes")
	}
	if c.GameQuery.MaximumRetries < 0 || c.GameQuery.MaximumRetries > 3 {
		return fmt.Errorf("sidero: game_query.maximum_retries must be between 0 and 3")
	}
	if c.GameQuery.StaleSeconds < c.GameQuery.CacheSeconds {
		return fmt.Errorf("sidero: game_query.stale_seconds cannot be less than cache_seconds")
	}
	if c.RateLimits.MaximumPerServerPerRoute > c.RateLimits.MaximumGlobalPerRoute {
		return fmt.Errorf("sidero: rate_limits.maximum_per_server_per_route cannot exceed maximum_global_per_route")
	}
	for _, sources := range [][]string{c.Firewall.AllowedSources, c.Firewall.BlockedSources} {
		for _, source := range sources {
			if _, err := netip.ParsePrefix(source); err != nil {
				return fmt.Errorf("sidero: firewall source policy contains an invalid CIDR")
			}
		}
	}
	return nil
}
