// Package capabilities implements Sidero protocol negotiation.
package capabilities

import "github.com/pterodactyl/wings/config"

const Version = "0.1.0"

type Limits struct {
	MaximumRemoteDownloadBytes int64 `json:"maximum_remote_download_bytes"`
	MaximumArchiveEntries      int   `json:"maximum_archive_entries"`
	MaximumSearchResults       int   `json:"maximum_search_results"`
	MaximumUploadBytes         int64 `json:"maximum_upload_bytes"`
	MaximumOperationGlobal     int   `json:"maximum_operation_global"`
	MaximumOperationPerServer  int   `json:"maximum_operation_per_server"`
	OperationRetentionSeconds  int   `json:"operation_retention_seconds"`
}

type Compatibility struct {
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}

type Response struct {
	ProtocolVersion int               `json:"protocol_version"`
	WingsVersion    string            `json:"wings_version"`
	SideroVersion   string            `json:"sidero_version"`
	Features        map[string]bool   `json:"features"`
	FeatureVersions map[string]string `json:"feature_versions"`
	Limits          Limits            `json:"limits"`
	Compatibility   Compatibility     `json:"compatibility"`
}

func Build(c config.SideroConfiguration, wingsVersion string) Response {
	enabled := c.Enabled
	features := map[string]bool{
		"operations":            enabled,
		"file_search":           enabled && c.Files.SearchEnabled,
		"content_search":        enabled && c.Files.ContentSearchEnabled,
		"remote_download":       enabled && c.RemoteDownload.Enabled,
		"archive_inspection":    enabled && c.Archives.InspectionEnabled,
		"safe_extraction":       enabled && c.Archives.InspectionEnabled,
		"resumable_upload":      enabled && c.Uploads.ResumableEnabled,
		"installer_operations":  enabled && c.Installers.Enabled,
		"resolved_modpack":      enabled && c.Installers.Enabled,
		"game_query_java":       enabled && c.GameQuery.Enabled,
		"game_query_bedrock":    enabled && c.GameQuery.Enabled,
		"file_probe":            enabled,
		"conditional_write":     enabled,
		"world_operations":      enabled && c.Worlds.Enabled,
		"process_exit_metadata": enabled,
		"network_statistics":    enabled && c.NetworkStatistics.Enabled,
		"firewall":              false,
	}
	state := "compatible"
	if !enabled {
		state = "disabled"
	}
	return Response{
		ProtocolVersion: c.ProtocolVersion,
		WingsVersion:    wingsVersion,
		SideroVersion:   Version,
		Features:        features,
		FeatureVersions: map[string]string{"operations": "1", "files": "1", "query": "1", "firewall": "1", "worlds": "1", "installers": "1"},
		Limits: Limits{
			MaximumRemoteDownloadBytes: c.RemoteDownload.MaximumBytes,
			MaximumArchiveEntries:      c.Archives.MaximumEntries,
			MaximumSearchResults:       c.Files.MaximumSearchResults,
			MaximumUploadBytes:         c.Uploads.MaximumUploadBytes,
			MaximumOperationGlobal:     c.Operations.MaximumConcurrentGlobal,
			MaximumOperationPerServer:  c.Operations.MaximumConcurrentPerServer,
			OperationRetentionSeconds:  c.Operations.RetentionSeconds,
		},
		Compatibility: Compatibility{State: state},
	}
}
