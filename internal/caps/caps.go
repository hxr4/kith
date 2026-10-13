// Package caps probes the local capability manifest and matches task
// requirements against it. Kith verifies; it never provisions.
package caps

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"

	"kith/internal/protocol"
	"kith/internal/task"
)

type GPU struct {
	Name       string  `json:"name"`
	VRAMGB     float64 `json:"vram_gb"`
	FreeVRAMGB float64 `json:"free_vram_gb"`
}

// Manifest is the advertised capability manifest (§10.1).
type Manifest struct {
	OS         string            `json:"os"`
	Arch       string            `json:"arch"`
	Python     []string          `json:"python"`
	Packages   map[string]string `json:"packages"`
	GPUs       []GPU             `json:"gpus"`
	FreeDiskGB float64           `json:"free_disk_gb"`
	RAMGB      float64           `json:"ram_gb"`
	Mode       string            `json:"mode"`
	QueueLen   int               `json:"queue_len"`
	Zstd       bool              `json:"zstd"`
	KithVer    string            `json:"kith_ver"`
}

// Request parameterises a probe.
type Request struct {
	Packages []string // distributions to look up via importlib.metadata
	DiskPath string   // where free space is measured (the workspace root)
}

// Source produces manifests. Production uses System; tests inject fakes.
type Source interface {
	Probe(ctx context.Context, req Request) (Manifest, error)
}

// DefaultPackages are probed for the advertised manifest.
var DefaultPackages = []string{"torch", "numpy"}

const GiB = 1 << 30

// Summary is a one-line human description used by list_peers.
func (m Manifest) Summary() string {
	parts := []string{m.OS + "/" + m.Arch}
	if len(m.Python) > 0 {
		parts = append(parts, "python "+strings.Join(m.Python, ","))
	}
	for _, g := range m.GPUs {
		parts = append(parts, fmt.Sprintf("%s %.0fGB", g.Name, g.VRAMGB))
	}
	parts = append(parts, fmt.Sprintf("disk %.0fGB free", m.FreeDiskGB))
	return strings.Join(parts, ", ")
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

// Check verifies static requirements (everything except the probe command
// and concurrency). inputsBytes feeds the disk precheck.
func Check(m Manifest, env task.Env, inputsBytes int64) *protocol.Error {
	if env.OS != "" && !strings.HasPrefix(strings.ToLower(m.OS), strings.ToLower(env.OS)) {
		return protocol.Errf(protocol.CodeNoMatch, "os %s required (found %s).", env.OS, m.OS).
			WithFix("submit this task to a peer running %s", env.OS)
	}
	if env.Arch != "" && NormArch(env.Arch) != NormArch(m.Arch) {
		return protocol.Errf(protocol.CodeNoMatch, "arch %s required (found %s).", env.Arch, m.Arch).
			WithFix("submit this task to a %s peer", env.Arch)
	}
	if env.Python != "" {
		ok := false
		for _, v := range m.Python {
			if good, _ := MatchVersion(env.Python, v); good {
				ok = true
				break
			}
		}
		if !ok {
			found := "none"
			if len(m.Python) > 0 {
				found = strings.Join(m.Python, ", ")
			}
			return protocol.Errf(protocol.CodeNoMatch, "python%s not found (found %s).", env.Python, found).
				WithFix("install Python %s on the executor and make it available as %s", env.Python, pythonHint(m.OS))
		}
	}
	names := make([]string, 0, len(env.Packages))
	for n := range env.Packages {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		c := env.Packages[name]
		have, ok := lookupPkg(m.Packages, name)
		if !ok {
			return protocol.Errf(protocol.CodeNoMatch, "%s%s not found (not installed).", name, c).WithFix("%s", PipFix(name, c))
		}
		good, err := MatchVersion(c, have)
		if err != nil {
			return protocol.Errf(protocol.CodeNoMatch, "bad constraint %s%s: %v", name, c, err).
				WithFix("use a constraint such as >=2.3 or >=2.3+cu121")
		}
		if !good {
			return protocol.Errf(protocol.CodeNoMatch, "%s%s not found (found %s).", name, c, have).WithFix("%s", PipFix(name, c))
		}
	}
	if env.GPU != nil && env.GPU.Required {
		ok := false
		for _, g := range m.GPUs {
			if g.VRAMGB+0.05 >= env.GPU.VRAMGB {
				ok = true
				break
			}
		}
		if !ok {
			found := "no NVIDIA GPU"
			if len(m.GPUs) > 0 {
				var gs []string
				for _, g := range m.GPUs {
					gs = append(gs, fmt.Sprintf("%s %.1fGB", g.Name, g.VRAMGB))
				}
				found = strings.Join(gs, ", ")
			}
			return protocol.Errf(protocol.CodeNoMatch, "GPU with >=%gGB VRAM required (found %s).", env.GPU.VRAMGB, found).
				WithFix("submit to a peer with a suitable NVIDIA GPU (check `nvidia-smi` there), or set gpu.required = false")
		}
	}
	need := float64(inputsBytes+GiB) / GiB
	if m.FreeDiskGB+1e-9 < need {
		return protocol.Errf(protocol.CodeDiskFull, "need %.1fGB free for inputs + 1GB headroom (found %.1fGB).", need, m.FreeDiskGB).
			WithFix("free disk space on the executor or reduce the task inputs")
	}
	return nil
}

func pythonHint(os string) string {
	if strings.HasPrefix(os, "windows") {
		return "`py -3` or `python`"
	}
	return "`python3`"
}

// lookupPkg compares distribution names case-insensitively with - and _ equal.
func lookupPkg(pk map[string]string, name string) (string, bool) {
	norm := func(s string) string { return strings.ReplaceAll(strings.ToLower(s), "_", "-") }
	for k, v := range pk {
		if norm(k) == norm(name) {
			return v, true
		}
	}
	return "", false
}

var cudaTag = regexp.MustCompile(`^cu\d+$`)

// PipFix returns a human remediation for a missing / wrong package.
func PipFix(name, constraint string) string {
	_, local := splitLocal(strings.TrimSpace(constraint))
	if cudaTag.MatchString(local) {
		return fmt.Sprintf("pip install %s --index-url https://download.pytorch.org/whl/%s", name, local)
	}
	c, _ := splitLocal(constraint)
	return fmt.Sprintf("pip install \"%s%s\"", name, c)
}

func splitLocal(v string) (string, string) {
	if i := strings.IndexByte(v, '+'); i >= 0 {
		return v[:i], v[i+1:]
	}
	return v, ""
}

var numericRE = regexp.MustCompile(`^v?(\d+(?:\.\d+)*)`)

// MatchVersion implements Kith's version rule (DECISIONS.md D-022):
//   - the numeric release (major.minor.patch) is compared with Masterminds/semver;
//     pre-release/dev suffixes on the installed version are ignored;
//   - if the constraint carries a local tag (+cu121) the installed version
//     must carry exactly that tag; without one, local tags are ignored.
func MatchVersion(constraint, installed string) (bool, error) {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" || constraint == "*" {
		return true, nil
	}
	cnum, clocal := splitLocal(constraint)
	inum, ilocal := splitLocal(strings.TrimSpace(installed))
	if clocal != "" && !strings.EqualFold(clocal, ilocal) {
		return false, nil
	}
	m := numericRE.FindStringSubmatch(inum)
	if m == nil {
		return false, fmt.Errorf("unparseable version %q", installed)
	}
	v, err := semver.NewVersion(m[1])
	if err != nil {
		return false, err
	}
	// PEP 440 spellings → Masterminds.
	cnum = strings.ReplaceAll(cnum, "~=", "~")
	cnum = strings.ReplaceAll(cnum, "==", "=")
	c, err := semver.NewConstraint(cnum)
	if err != nil {
		return false, err
	}
	return c.Check(v), nil
}

// NormArch maps Go/uname arch names to manifest names.
func NormArch(a string) string {
	switch strings.ToLower(a) {
	case "amd64", "x86_64", "x64":
		return "x86_64"
	case "arm64", "aarch64":
		return "arm64"
	}
	return strings.ToLower(a)
}
