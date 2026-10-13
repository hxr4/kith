package caps

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"kith/internal/config"
	"kith/internal/protocol"
)

// System probes the real machine by shelling out (never importing drivers).
type System struct{}

var _ Source = System{}

func (System) Probe(ctx context.Context, req Request) (Manifest, error) {
	m := Manifest{
		OS:       osName(),
		Arch:     NormArch(runtime.GOARCH),
		Packages: map[string]string{},
		GPUs:     []GPU{},
		Mode:     "available",
		Zstd:     true,
		KithVer:  config.KithVersion,
	}
	if req.DiskPath != "" {
		if b, err := freeDisk(req.DiskPath); err == nil {
			m.FreeDiskGB = round1(float64(b) / GiB)
		}
	}
	if b, err := totalRAM(); err == nil {
		m.RAMGB = round1(float64(b) / GiB)
	}
	m.GPUs = probeGPUs(ctx)
	pys := pythonCandidates()
	var first []string
	seen := map[string]bool{}
	for _, argv := range pys {
		v := pythonVersion(ctx, argv)
		if v == "" {
			continue
		}
		if first == nil {
			first = argv
		}
		if !seen[v] {
			seen[v] = true
			m.Python = append(m.Python, v)
		}
	}
	if first != nil {
		pk := append([]string{}, req.Packages...)
		if len(pk) > 0 {
			m.Packages = probePackages(ctx, first, pk)
		}
	}
	return m, nil
}

func pythonCandidates() [][]string {
	if runtime.GOOS == "windows" {
		return [][]string{{"py", "-3"}, {"python"}}
	}
	return [][]string{{"python3"}, {"python"}}
}

var pyVerRE = regexp.MustCompile(`Python (\d+\.\d+\.\d+)`)

func run(ctx context.Context, timeout time.Duration, argv ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, argv[1:]...)
	hideWindow(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	return out.Bytes(), err
}

func pythonVersion(ctx context.Context, argv []string) string {
	out, err := run(ctx, 15*time.Second, append(append([]string{}, argv...), "--version")...)
	if err != nil {
		return ""
	}
	m := pyVerRE.FindSubmatch(out)
	if m == nil {
		return ""
	}
	return string(m[1])
}

const pkgScript = `import json,sys
from importlib import metadata as m
o={}
for d in sys.argv[1:]:
    try:
        o[d]=m.version(d)
    except Exception:
        pass
print(json.dumps(o))`

func probePackages(ctx context.Context, py []string, pkgs []string) map[string]string {
	argv := append(append(append([]string{}, py...), "-c", pkgScript), pkgs...)
	out, err := run(ctx, 60*time.Second, argv...)
	res := map[string]string{}
	if err != nil {
		return res
	}
	// The JSON is the last line.
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	json.Unmarshal([]byte(lines[len(lines)-1]), &res)
	return res
}

func probeGPUs(ctx context.Context) []GPU {
	out, err := run(ctx, 20*time.Second, "nvidia-smi", "--query-gpu=name,memory.total,memory.free", "--format=csv,noheader")
	if err != nil {
		return []GPU{}
	}
	return ParseNvidiaSMI(string(out))
}

// ParseNvidiaSMI parses `nvidia-smi --query-gpu=name,memory.total,memory.free --format=csv,noheader`.
func ParseNvidiaSMI(out string) []GPU {
	gpus := []GPU{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, ",")
		if len(f) != 3 {
			continue
		}
		tot, err1 := parseMiB(f[1])
		free, err2 := parseMiB(f[2])
		if err1 != nil || err2 != nil {
			continue
		}
		name := strings.TrimSpace(f[0])
		name = strings.TrimPrefix(name, "NVIDIA ")
		name = strings.TrimPrefix(name, "GeForce ")
		gpus = append(gpus, GPU{Name: name, VRAMGB: round1(tot / 1024), FreeVRAMGB: round1(free / 1024)})
	}
	return gpus
}

func parseMiB(s string) (float64, error) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "MiB"))
	return strconv.ParseFloat(s, 64)
}

// RunProbe runs a task's probe argv in a scratch directory. Never a shell.
func RunProbe(ctx context.Context, argv []string, scratch string) *protocol.Error {
	if len(argv) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return protocol.Errf(protocol.CodeNoMatch, "probe command %q not found on executor PATH.", argv[0]).
			WithFix("install %s on the executor or change env.probe", argv[0])
	}
	cmd := exec.CommandContext(ctx, path, argv[1:]...)
	cmd.Dir = scratch
	cmd.Env = os.Environ()
	hideWindow(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(out.String())
		if len(tail) > 400 {
			tail = "…" + tail[len(tail)-400:]
		}
		if ctx.Err() != nil {
			return protocol.Errf(protocol.CodeNoMatch, "probe %q timed out after 60s.", strings.Join(argv, " ")).
				WithFix("make env.probe finish quickly on the executor")
		}
		return protocol.Errf(protocol.CodeNoMatch, "probe %q failed: %v. Output: %s", strings.Join(argv, " "), err, tail).
			WithFix("run the probe on the executor by hand and fix its environment")
	}
	return nil
}

func osName() string {
	switch runtime.GOOS {
	case "darwin":
		out, err := run(context.Background(), 5*time.Second, "sw_vers", "-productVersion")
		if err == nil {
			return "macos-" + strings.TrimSpace(string(out))
		}
		return "macos"
	case "windows":
		return windowsName()
	default:
		return runtime.GOOS
	}
}
