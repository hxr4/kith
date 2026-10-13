package main

import "encoding/json"

// The GUI only talks to the daemon over IPC, like the CLI. These minimal
// views decode just what the GUI's own logic (notifications, tray) needs;
// everything else is passed to the frontend as raw JSON.

type taskLite struct {
	ID              string `json:"id"`
	Role            string `json:"role"`
	State           string `json:"state"`
	Name            string `json:"name"`
	PeerName        string `json:"peer_name"`
	Detail          string `json:"detail"`
	PendingApproval bool   `json:"pending_approval"`
}

type peerLite struct {
	FP        string `json:"fp"`
	Name      string `json:"name"`
	Reachable bool   `json:"reachable"`
	Caps      string `json:"caps"`
}

type statusLite struct {
	FP      string `json:"fp"`
	Display string `json:"display"`
	Name    string `json:"name"`
	Port    int    `json:"port"`
	Version string `json:"version"`
	PID     int    `json:"pid"`
}

// Snapshot is what the frontend renders; pushed as the "kith:state" event.
type Snapshot struct {
	Running  bool            `json:"running"`
	Error    string          `json:"error,omitempty"`
	Service  string          `json:"service,omitempty"`
	Status   json.RawMessage `json:"status,omitempty"`
	Peers    json.RawMessage `json:"peers,omitempty"`
	Tasks    json.RawMessage `json:"tasks,omitempty"`
	Pending  int             `json:"pending"`
	Active   int             `json:"active"`
	Lite     bool            `json:"lite"`
	Platform string          `json:"platform"`
	GUIVer   string          `json:"gui_version"`
}

func isTerminal(s string) bool {
	return s == "COMPLETED" || s == "FAILED" || s == "CANCELLED" || s == "REJECTED"
}

func needsApproval(t taskLite) bool {
	return t.Role == "executor" && t.State == "SUBMITTED" && t.PendingApproval
}
