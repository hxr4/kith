// Package artifacts collects task outputs into content-addressed artifact
// records and decides copy-back versus retain-on-executor (§11).
package artifacts

import (
	"os"
	"path/filepath"
	"strings"

	"kith/internal/store"
	"kith/internal/task"
	"kith/internal/transfer"
)

const GiB = 1 << 30

// Collected is the result of globbing a finished workspace.
type Collected struct {
	Artifacts  []store.Artifact
	TotalBytes int64
	CopyBack   bool // false → retained on the executor, fetched on demand
}

// Collect globs include patterns in ws, hashes every match and decides
// whether the set is copied back (copyBack && total <= retainCapGB).
// Literal paths that do not exist are skipped (outputs are optional).
func Collect(ws string, include []string, copyBack bool, retainCapGB float64, producerFP, taskID string) (Collected, error) {
	var pats []string
	for _, p := range include {
		if !strings.ContainsAny(p, "*?[") {
			if _, err := os.Stat(filepath.Join(ws, filepath.FromSlash(p))); err != nil {
				continue
			}
		}
		pats = append(pats, p)
	}
	files, err := task.ExpandGlobs(ws, pats)
	if err != nil {
		return Collected{}, err
	}
	var c Collected
	for _, f := range files {
		sha, size, err := transfer.SHAFile(filepath.Join(ws, filepath.FromSlash(f)))
		if err != nil {
			return Collected{}, err
		}
		c.Artifacts = append(c.Artifacts, store.Artifact{TaskID: taskID, Path: f, SHA256: sha, Size: size,
			Location: store.LocLocal, ProducerFP: producerFP})
		c.TotalBytes += size
	}
	c.CopyBack = copyBack && float64(c.TotalBytes) <= retainCapGB*GiB
	for i := range c.Artifacts {
		c.Artifacts[i].CopyBack = c.CopyBack
	}
	return c, nil
}

// Status renders the registry condition of an artifact as seen locally.
func Status(a store.Artifact, producerOnline bool) (status string, producerOffline bool) {
	if a.Location != store.LocRemote {
		return a.Location, false
	}
	if !producerOnline {
		return "remote (producer offline)", true
	}
	return store.LocRemote, false
}

// PendingCopyBack reports whether any artifact still has to be copied back.
func PendingCopyBack(as []store.Artifact) bool {
	for _, a := range as {
		if a.CopyBack && a.Location == store.LocRemote {
			return true
		}
	}
	return false
}
