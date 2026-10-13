package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestTaskLifecycleAndIdempotency(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tk := Task{ID: "tsk_01J00000000000000000000000", Role: RoleExecutor, PeerFP: "p", Project: "x", Name: "n", SpecJSON: "{}", State: "SUBMITTED"}
	got, created, err := s.CreateTask(ctx, tk)
	if err != nil || !created || got.Attempt != 1 {
		t.Fatal(err, created)
	}
	_, created, err = s.CreateTask(ctx, tk)
	if err != nil || created {
		t.Fatal("duplicate create must return existing")
	}
	_, seq, err := s.Transition(ctx, tk.ID, Change{State: "ACCEPTED"}, nil)
	if err != nil || seq != 2 {
		t.Fatal(err, seq)
	}
	s.Close()

	// Reopen: everything committed survives.
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	g, err := s.GetTask(ctx, tk.ID)
	if err != nil || g.State != "ACCEPTED" {
		t.Fatal(err, g.State)
	}
	tr, _ := s.Transitions(ctx, tk.ID)
	if len(tr) != 2 || tr[0].State != "SUBMITTED" || tr[1].State != "ACCEPTED" {
		t.Fatalf("%+v", tr)
	}
	l, _ := s.ListTasks(ctx, TaskFilter{States: []string{"ACCEPTED", "RUNNING"}})
	if len(l) != 1 {
		t.Fatal(len(l))
	}
}

func TestPeersArtifactsTransfers(t *testing.T) {
	ctx := context.Background()
	s, err := OpenPath(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.UpsertPeer(ctx, Peer{FP: "aa", Name: "win", Policy: "ask"}); err != nil {
		t.Fatal(err)
	}
	s.TouchPeer(ctx, "aa", "10.0.0.2:7420")
	p, _ := s.GetPeer(ctx, "aa")
	if p.LastAddr != "10.0.0.2:7420" || p.Policy != "ask" {
		t.Fatalf("%+v", p)
	}
	if err := s.DeletePeer(ctx, "aa"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPeer(ctx, "aa"); err != ErrNotFound {
		t.Fatal(err)
	}
	a := Artifact{TaskID: "t", Path: "out/model.pt", SHA256: "h", Size: 3, Location: LocRemote, ProducerFP: "aa", CopyBack: true}
	s.UpsertArtifact(ctx, a)
	s.MarkArtifactLocal(ctx, "t", "out/model.pt")
	s.UpsertArtifact(ctx, a) // re-offer with same hash keeps local
	g, _ := s.GetArtifact(ctx, "t", "out/model.pt")
	if g.Location != LocLocal || !g.Fetched {
		t.Fatalf("%+v", g)
	}
	s.UpsertTransfer(ctx, Transfer{TaskID: "t", SHA256: "h", Path: "a", Size: 10, BytesDone: 4, Direction: DirIn})
	s.UpsertTransfer(ctx, Transfer{TaskID: "t", SHA256: "h", Path: "a", Size: 10, BytesDone: 8, Direction: DirIn})
	tr, _ := s.GetTransfer(ctx, "t", "h", DirIn)
	if tr.BytesDone != 8 {
		t.Fatal(tr.BytesDone)
	}
}
