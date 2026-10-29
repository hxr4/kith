package mcp

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"kith/internal/daemon"
)

// MetricPrefix starts a machine-readable progress line in a task's output:
//
//	KITH_METRIC {"kind":"epoch","epoch":3,"epochs":20,"val_acc":0.81,"macro_f1":0.80}
//
// Records with an "epoch" key (and no other kind) form the history; kind
// "step" is the latest in-epoch progress; any other kind is kept by name.
const MetricPrefix = "KITH_METRIC "

// progress is what task_progress learns from one task's log, built
// incrementally so polling never re-reads the whole log.
type progress struct {
	mu       sync.Mutex
	offset   int64
	partial  string // an unterminated last line
	epochs   []map[string]any
	step     map[string]any
	kinds    map[string]map[string]any // start, done, ... (latest of each)
	tail     []string                  // recent human-readable lines
	explicit bool                      // the task prints KITH_METRIC lines
}

const (
	maxEpochs = 500
	maxTail   = 200
)

type progressStore struct {
	mu sync.Mutex
	m  map[string]*progress
}

func newProgressStore() *progressStore { return &progressStore{m: map[string]*progress{}} }

func (s *progressStore) get(id string) *progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.m[id]
	if p == nil {
		p = &progress{kinds: map[string]map[string]any{}}
		s.m[id] = p
	}
	return p
}

// update reads any new log text and folds it in. It returns the latest
// LogsResult (for state and completeness).
func (p *progress) update(ctx context.Context, b Backend, id string) (daemon.LogsResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var last daemon.LogsResult
	for i := 0; i < 64; i++ { // at most 64 x 256 KiB per call
		r, err := b.Logs(ctx, id, p.offset)
		if err != nil {
			return last, err
		}
		last = r
		if r.Data == "" || r.Offset <= p.offset {
			break
		}
		p.offset = r.Offset
		p.feed(r.Data)
	}
	if last.Done && p.partial != "" {
		p.line(p.partial)
		p.partial = ""
	}
	return last, nil
}

func (p *progress) feed(s string) {
	s = p.partial + s
	lines := strings.Split(s, "\n")
	p.partial = lines[len(lines)-1]
	for _, l := range lines[:len(lines)-1] {
		p.line(strings.TrimRight(l, "\r"))
	}
}

func (p *progress) line(l string) {
	if i := strings.Index(l, MetricPrefix); i >= 0 {
		var m map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(l[i+len(MetricPrefix):])), &m) == nil {
			if !p.explicit {
				p.explicit = true
				p.epochs = nil // prefer the task's own records over guesses
				p.step = nil
			}
			p.record(m)
			return
		}
	}
	p.tail = append(p.tail, l)
	if len(p.tail) > maxTail {
		p.tail = p.tail[len(p.tail)-maxTail:]
	}
	if !p.explicit {
		if m := guessMetrics(l); m != nil {
			p.record(m)
		}
	}
}

func (p *progress) record(m map[string]any) {
	kind, _ := m["kind"].(string)
	delete(m, "kind")
	switch {
	case kind == "step":
		p.step = m
	case kind == "epoch" || (kind == "" && m["epoch"] != nil):
		// A resumed run repeats epochs; keep the newest record for each.
		if n := len(p.epochs); n > 0 && num(p.epochs[n-1]["epoch"]) >= num(m["epoch"]) {
			k := n
			for k > 0 && num(p.epochs[k-1]["epoch"]) >= num(m["epoch"]) {
				k--
			}
			p.epochs = p.epochs[:k]
		}
		p.epochs = append(p.epochs, m)
		if len(p.epochs) > maxEpochs {
			p.epochs = p.epochs[len(p.epochs)-maxEpochs:]
		}
		p.step = nil
	case kind != "":
		p.kinds[kind] = m
	}
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

var (
	epochRe = regexp.MustCompile(`(?i)\bepoch\b[\s:=#]*(\d+)(?:\s*/\s*(\d+))?`)
	kvRe    = regexp.MustCompile(`(?i)\b([a-z][a-z0-9_@.\-]*)\s*[=:]\s*(-?\d+(?:\.\d+)?(?:e-?\d+)?)\b`)
)

// guessMetrics reads "epoch 3/20 loss=0.41 val_acc: 0.88"-style lines from
// tasks that don't print KITH_METRIC records.
func guessMetrics(l string) map[string]any {
	em := epochRe.FindStringSubmatch(l)
	if em == nil {
		return nil
	}
	m := map[string]any{}
	if v, err := strconv.ParseFloat(em[1], 64); err == nil {
		m["epoch"] = v
	}
	if em[2] != "" {
		if v, err := strconv.ParseFloat(em[2], 64); err == nil {
			m["epochs"] = v
		}
	}
	for _, kv := range kvRe.FindAllStringSubmatch(l, -1) {
		k := strings.ToLower(kv[1])
		if k == "epoch" || k == "epochs" {
			continue
		}
		if v, err := strconv.ParseFloat(kv[2], 64); err == nil {
			m[k] = v
		}
	}
	if len(m) < 2 {
		return nil
	}
	return m
}

// lowerIsBetter reports whether a metric improves downward.
func lowerIsBetter(k string) bool {
	k = strings.ToLower(k)
	for _, s := range []string{"loss", "err", "perplexity", "ppl", "mae", "mse", "rmse", "wer", "cer"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// best is the best value of each numeric metric across the epoch history,
// with the epoch it came from.
func (p *progress) best() map[string]any {
	out := map[string]any{}
	skip := map[string]bool{"epoch": true, "epochs": true, "lr": true, "eta_s": true, "epoch_s": true, "img_s": true, "best_val_acc": true, "peak_vram_gb": true}
	for _, e := range p.epochs {
		for k, v := range e {
			f, ok := v.(float64)
			if !ok || skip[k] {
				continue
			}
			cur, seen := out[k].(map[string]any)
			if !seen || (lowerIsBetter(k) && f < cur["value"].(float64)) || (!lowerIsBetter(k) && f > cur["value"].(float64)) {
				out[k] = map[string]any{"value": f, "epoch": e["epoch"]}
			}
		}
	}
	return out
}

// snapshot is the agent-facing summary. tail is the number of recent
// output lines to include; recent the number of epoch records.
func (p *progress) snapshot(tail, recent int) map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]any{"epochs_reported": len(p.epochs), "metrics_source": "none"}
	if len(p.epochs) > 0 || p.step != nil {
		out["metrics_source"] = map[bool]string{true: "KITH_METRIC", false: "parsed from output"}[p.explicit]
	}
	if n := len(p.epochs); n > 0 {
		out["latest"] = p.epochs[n-1]
		out["best"] = p.best()
		if recent > 0 {
			out["recent"] = p.epochs[max(0, n-recent):]
		}
	}
	if p.step != nil {
		out["current_step"] = p.step
	}
	for k, v := range p.kinds {
		out[k] = v
	}
	if tail > 0 {
		t := p.tail
		if p.partial != "" {
			t = append(append([]string{}, t...), p.partial)
		}
		if len(t) > 0 {
			out["tail"] = t[max(0, len(t)-tail):]
		}
	}
	return out
}

// history returns every epoch record (for plotting or a final report).
func (p *progress) history() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]map[string]any{}, p.epochs...)
}
