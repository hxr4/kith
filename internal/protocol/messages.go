package protocol

import "encoding/json"

// Message type names (§7.2).
const (
	MsgHello          = "hello"
	MsgCaps           = "caps"
	MsgTaskSubmit     = "task.submit"
	MsgTaskAccept     = "task.accept"
	MsgTaskReject     = "task.reject"
	MsgFileOffer      = "file.offer"
	MsgFileRequest    = "file.request"
	MsgFileDone       = "file.done"
	MsgFileError      = "file.error" // addition, see DECISIONS.md D-010
	MsgTaskState      = "task.state"
	MsgTaskCancel     = "task.cancel"
	MsgArtifactOffer  = "artifact.offer"
	MsgSyncRequest    = "sync.request"
	MsgSyncState      = "sync.state"
	MsgPairBegin      = "pair.begin"
	MsgPairConfirm    = "pair.confirm"
	MsgPairIdentities = "pair.identities"
)

type Hello struct {
	Name     string `json:"name"`
	FP       string `json:"fp"`
	KithVer  string `json:"kith_ver"`
	Unpaired bool   `json:"unpaired,omitempty"`
	Port     int    `json:"port,omitempty"` // TCP listen port, used to learn last_addr
	Zstd     bool   `json:"zstd,omitempty"` // compression negotiation (§11)
}

type GoAway struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type FileOffer struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type TaskSubmit struct {
	Spec json.RawMessage `json:"spec"`
}

type TaskAccept struct {
	TaskID          string `json:"task_id"`
	AcceptedAt      string `json:"accepted_at"`
	PendingApproval bool   `json:"pending_approval"`
	State           string `json:"state,omitempty"`
	Seq             int    `json:"seq,omitempty"`
}

type TaskReject struct {
	TaskID      string `json:"task_id"`
	Code        string `json:"code"`
	Message     string `json:"message,omitempty"`
	Remediation string `json:"remediation,omitempty"`
	QueueLen    *int   `json:"queue_len,omitempty"`
	Seq         int    `json:"seq,omitempty"`
}

type FileOfferMsg struct {
	TaskID string      `json:"task_id"`
	Files  []FileOffer `json:"files"`
}

type FileRequest struct {
	TaskID string `json:"task_id"`
	SHA256 string `json:"sha256"`
	Offset int64  `json:"offset"`
}

type FileDone struct {
	TaskID string `json:"task_id"`
	SHA256 string `json:"sha256"`
	OK     bool   `json:"ok"`
}

type FileError struct {
	TaskID  string `json:"task_id"`
	SHA256  string `json:"sha256"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type TaskState struct {
	TaskID          string `json:"task_id"`
	State           string `json:"state"`
	Detail          string `json:"detail,omitempty"`
	Attempt         int    `json:"attempt"`
	TS              string `json:"ts"`
	Seq             int    `json:"seq,omitempty"`
	PendingApproval bool   `json:"pending_approval,omitempty"`
	Code            string `json:"code,omitempty"`
	Remediation     string `json:"remediation,omitempty"`
	LogSize         int64  `json:"log_size,omitempty"`
}

type TaskCancel struct {
	TaskID string `json:"task_id"`
}

type Artifact struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Remote bool   `json:"remote"`
}

type ArtifactOffer struct {
	TaskID    string     `json:"task_id"`
	Artifacts []Artifact `json:"artifacts"`
}

type SyncRequest struct {
	// TaskIDs the sender mirrors as non-terminal; the executor answers for them
	// too, so tasks that finished during a partition are reconciled.
	TaskIDs []string `json:"task_ids,omitempty"`
}

type SyncState struct {
	Tasks     []TaskState     `json:"tasks"`
	Artifacts []ArtifactOffer `json:"artifacts"`
	// Unknown lists requested task ids the executor has no record of; the
	// requester re-sends task.submit for them (idempotent).
	Unknown []string `json:"unknown,omitempty"`
}

type PairBegin struct {
	Msg []byte `json:"msg"`
}

type PairConfirm struct {
	MAC []byte `json:"mac"`
}

type PairIdentities struct {
	Name    string `json:"name"`
	PubKey  []byte `json:"pubkey"`
	FP      string `json:"fingerprint"`
	KithVer string `json:"kith_ver"`
	Port    int    `json:"port,omitempty"`
}
