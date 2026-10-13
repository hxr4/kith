package audit

import "testing"

func TestAppendAndRead(t *testing.T) {
	home := t.TempDir()
	l, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	l.Write(Event{Event: EvSubmit, TaskID: "tsk_x", PeerFP: "fp", Argv: []string{"python", "train.py"}})
	l.Write(Event{Event: EvState, TaskID: "tsk_x", State: "RUNNING"})
	l.Close()
	l, _ = Open(home) // reopen appends, never truncates
	l.Write(Event{Event: EvState, TaskID: "tsk_x", State: "COMPLETED"})
	l.Close()
	ev, err := ReadAll(home)
	if err != nil || len(ev) != 3 {
		t.Fatal(err, len(ev))
	}
	if ev[0].Argv[1] != "train.py" || ev[2].State != "COMPLETED" || ev[0].TS == "" {
		t.Fatalf("%+v", ev)
	}
}
