// Package process defines safe process-exit metadata and bounded relative-path discovery.
package process

import (
	iofs "io/fs"
	"path"
	"time"

	wfs "github.com/pterodactyl/wings/server/filesystem"
)

type ExitMetadata struct {
	ExitCode         uint32    `json:"exit_code"`
	Signal           string    `json:"signal,omitempty"`
	Expected         bool      `json:"expected"`
	CrashDetected    bool      `json:"crash_detected"`
	OOMKilled        bool      `json:"oom_killed"`
	RestartAttempted bool      `json:"restart_attempted"`
	RestartAttempts  int       `json:"restart_attempts"`
	RestartExhausted bool      `json:"restart_exhausted"`
	CrashReport      string    `json:"crash_report,omitempty"`
	LatestLog        string    `json:"latest_log,omitempty"`
	Timestamp        time.Time `json:"timestamp"`
	UptimeMS         int64     `json:"uptime_ms"`
}

func DiscoverLatestPaths(filesystem *wfs.Filesystem, maximumEntries int) (string, string) {
	latestLog := regularRelativePath(filesystem, "logs/latest.log")
	entries, err := filesystem.ReadDir("crash-reports")
	if err != nil || maximumEntries < 1 {
		return "", latestLog
	}
	var newest string
	var newestTime time.Time
	for i, entry := range entries {
		if i >= maximumEntries {
			break
		}
		rel := path.Join("crash-reports", entry.Name())
		info, err := filesystem.UnixFS().Lstat(rel)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&iofs.ModeSymlink != 0 {
			continue
		}
		if newest == "" || info.ModTime().After(newestTime) {
			newest, newestTime = rel, info.ModTime()
		}
	}
	return newest, latestLog
}

func regularRelativePath(filesystem *wfs.Filesystem, relative string) string {
	info, err := filesystem.UnixFS().Lstat(relative)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&iofs.ModeSymlink != 0 {
		return ""
	}
	return relative
}
