// Package cleanup removes only Sidero-owned, generated staging directories.
package cleanup

import (
	"context"
	iofs "io/fs"
	"path"
	"time"

	"github.com/google/uuid"

	wfs "github.com/pterodactyl/wings/server/filesystem"
)

var stagingKinds = [...]string{"downloads", "archives", "uploads", "writes", "installers", "modpacks", "worlds"}

func Staging(ctx context.Context, filesystem *wfs.Filesystem, now time.Time, minimumAge time.Duration, maximum int) int {
	if filesystem == nil || minimumAge <= 0 || maximum < 1 {
		return 0
	}
	cutoff := now.Add(-minimumAge)
	removed := 0
	for _, kind := range stagingKinds {
		if removed >= maximum || ctx.Err() != nil {
			break
		}
		root := path.Join(".sidero", kind)
		entries, err := filesystem.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if removed >= maximum || ctx.Err() != nil {
				break
			}
			if _, err := uuid.Parse(entry.Name()); err != nil {
				continue
			}
			candidate := path.Join(root, entry.Name())
			info, err := filesystem.UnixFS().Lstat(candidate)
			if err != nil || !info.IsDir() || info.Mode()&iofs.ModeSymlink != 0 || info.ModTime().After(cutoff) {
				continue
			}
			if err := filesystem.Delete(candidate); err == nil {
				removed++
			}
		}
	}
	return removed
}
