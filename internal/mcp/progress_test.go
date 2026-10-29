package mcp

import (
	"context"
	"testing"

	"kith/internal/daemon"
)

// logsOnly serves a fixed log in chunks.
type logsOnly struct {
	Backend
	log   string
	chunk int
}

func (b logsOnly) Logs(_ context.Context, id string, off int64) (daemon.LogsResult, error) {
	end := min(int(off)+b.chunk, len(b.log))
	return daemon.LogsResult{TaskID: id, Offset: int64(end), Data: b.log[off:end], Done: end == len(b.log), State: "COMPLETED"}, nil
}

func TestProgressExplicitMetrics(t *testing.T) {
	log := "device: RTX 3050\n" +
		`KITH_METRIC {"kind":"start","epochs":3,"batch":256}` + "\n" +
		`KITH_METRIC {"kind":"step","epoch":1,"step":50,"steps":196,"loss":1.9}` + "\n" +
		"epoch 1/3 loss=1.5 val_acc=0.40\n" + // ignored once KITH_METRIC is seen
		`KITH_METRIC {"kind":"epoch","epoch":1,"epochs":3,"val_acc":0.40,"macro_f1":0.38,"val_loss":1.6}` + "\n" +
		`KITH_METRIC {"kind":"epoch","epoch":2,"epochs":3,"val_acc":0.71,"macro_f1":0.70,"val_loss":0.9}` + "\n" +
		"resumed from checkpoint after epoch 1\n" +
		`KITH_METRIC {"kind":"epoch","epoch":2,"epochs":3,"val_acc":0.69,"macro_f1":0.69,"val_loss":1.0}` + "\n" +
		`KITH_METRIC {"kind":"epoch","epoch":3,"epochs":3,"val_acc":0.65,"macro_f1":0.64,"val_loss":1.1}` + "\n" +
		`KITH_METRIC {"kind":"done","final_val_acc":0.65}` + "\n" +
		"done" // no trailing newline
	for _, chunk := range []int{7, 64, 1 << 20} { // lines split across reads
		p := newProgressStore().get("t")
		for done := false; !done; { // each poll reads a bounded amount
			r, err := p.update(context.Background(), logsOnly{log: log, chunk: chunk}, "t")
			if err != nil {
				t.Fatal(err)
			}
			done = r.Done
		}
		snap := p.snapshot(3, 2)
		if snap["metrics_source"] != "KITH_METRIC" || snap["epochs_reported"] != 3 {
			t.Fatalf("chunk %d: %+v", chunk, snap)
		}
		if l := snap["latest"].(map[string]any); l["epoch"] != 3.0 || l["val_acc"] != 0.65 {
			t.Fatalf("latest %+v", l)
		}
		best := snap["best"].(map[string]any)
		if b := best["val_acc"].(map[string]any); b["value"] != 0.69 || b["epoch"] != 2.0 {
			t.Fatalf("best val_acc should come from the resumed epoch 2: %+v", b)
		}
		if b := best["val_loss"].(map[string]any); b["value"] != 1.0 {
			t.Fatalf("loss improves downward: %+v", b)
		}
		if snap["start"] == nil || snap["done"] == nil || snap["current_step"] != nil {
			t.Fatalf("kinds: %+v", snap)
		}
		tail := snap["tail"].([]string)
		if tail[len(tail)-1] != "done" {
			t.Fatalf("tail %q", tail)
		}
	}
}

func TestProgressParsedMetrics(t *testing.T) {
	log := "loading\nEpoch 1/10 - loss: 0.912 - acc: 0.71 - val_f1=0.66\n[epoch 2] train_loss=0.55 F1: 0.74\nsaving\n"
	p := newProgressStore().get("t")
	p.update(context.Background(), logsOnly{log: log, chunk: 1 << 20}, "t")
	snap := p.snapshot(0, 5)
	if snap["metrics_source"] != "parsed from output" || snap["epochs_reported"] != 2 {
		t.Fatalf("%+v", snap)
	}
	r := snap["recent"].([]map[string]any)
	if r[0]["epochs"] != 10.0 || r[0]["loss"] != 0.912 || r[0]["val_f1"] != 0.66 || r[1]["f1"] != 0.74 {
		t.Fatalf("%+v", r)
	}
}
