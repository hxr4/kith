package main

import "fmt"

// Notice is something worth a desktop notification.
type Notice struct {
	Kind   string // "approval" | "finished"
	TaskID string
	Title  string
	Body   string
}

// diffNotices compares two polls and returns new notifications. The first
// poll (prev == nil) only establishes a baseline so starting the GUI never
// replays history.
func diffNotices(prev, cur []taskLite) []Notice {
	if prev == nil {
		return nil
	}
	before := make(map[string]taskLite, len(prev))
	for _, t := range prev {
		before[t.ID] = t
	}
	var out []Notice
	for _, t := range cur {
		old, seen := before[t.ID]
		if needsApproval(t) && (!seen || !needsApproval(old)) {
			out = append(out, Notice{Kind: "approval", TaskID: t.ID,
				Title: fmt.Sprintf("%s wants to run a task", orUnknown(t.PeerName)),
				Body:  fmt.Sprintf("%s (%s). Approve or deny?", t.Name, short(t.ID))})
			continue
		}
		// Tasks this machine submitted that just finished.
		if t.Role == "requester" && isTerminal(t.State) && seen && !isTerminal(old.State) {
			body := fmt.Sprintf("%s on %s", t.Name, orUnknown(t.PeerName))
			if t.Detail != "" {
				body += ": " + t.Detail
			}
			out = append(out, Notice{Kind: "finished", TaskID: t.ID, Title: "Task " + t.State, Body: body})
		}
	}
	return out
}

func orUnknown(s string) string {
	if s == "" {
		return "A peer"
	}
	return s
}

func short(id string) string {
	if len(id) > 14 {
		return id[:14]
	}
	return id
}
