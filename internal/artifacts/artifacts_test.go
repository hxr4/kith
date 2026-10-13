package artifacts

import (
	"os"
	"path/filepath"
	"testing"

	"kith/internal/store"
)

func write(t *testing.T, root, rel string, n int) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, make([]byte, n), 0o644)
}

func TestCollectCopyBackAndRetain(t *testing.T) {
	ws := t.TempDir()
	write(t, ws, "out/model.pt", 3000)
	write(t, ws, "out/metrics.json", 10)
	write(t, ws, "logs/a/b/train.log", 5)
	write(t, ws, "logs/other.txt", 5)
	inc := []string{"out/model.pt", "out/metrics.json", "logs/**/*.log", "out/missing.bin"}
	c, err := Collect(ws, inc, true, 10, "fp", "tsk_x")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Artifacts) != 3 || c.TotalBytes != 3015 || !c.CopyBack {
		t.Fatalf("%+v", c)
	}
	for _, a := range c.Artifacts {
		if len(a.SHA256) != 64 || !a.CopyBack || a.Location != store.LocLocal || a.ProducerFP != "fp" {
			t.Fatalf("%+v", a)
		}
	}
	// Over the cap → retained on the executor.
	c, _ = Collect(ws, inc, true, 1000.0/GiB, "fp", "tsk_x")
	if c.CopyBack || c.Artifacts[0].CopyBack {
		t.Fatal("expected retain")
	}
	// copy_back=false → retained regardless of size.
	c, _ = Collect(ws, inc, false, 10, "fp", "tsk_x")
	if c.CopyBack {
		t.Fatal("expected retain")
	}
	if !PendingCopyBack([]store.Artifact{{CopyBack: true, Location: store.LocRemote}}) ||
		PendingCopyBack([]store.Artifact{{CopyBack: true, Location: store.LocLocal}}) {
		t.Fatal("pending copy-back")
	}
	if s, off := Status(store.Artifact{Location: store.LocRemote}, false); s != "remote (producer offline)" || !off {
		t.Fatal(s)
	}
}
