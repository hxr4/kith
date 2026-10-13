package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/oklog/ulid/v2"

	"kith/internal/protocol"
)

var statFn = os.Stat

// Spec is a task specification (TOML [task] table, JSON on the wire).
type Spec struct {
	ID      string   `json:"id" toml:"id"`
	Project string   `json:"project" toml:"project"`
	Name    string   `json:"name" toml:"name"`
	Command []string `json:"command" toml:"command"`
	Inputs  Inputs   `json:"inputs" toml:"inputs"`
	Env     Env      `json:"env" toml:"env"`
	Outputs Outputs  `json:"outputs" toml:"outputs"`
	Policy  Policy   `json:"policy" toml:"policy"`
}

type Inputs struct {
	Files      []string `json:"files" toml:"files"`
	MaxTotalGB float64  `json:"max_total_gb" toml:"max_total_gb"`
	// Offers are filled at submit time: resolved paths with size and hash
	// snapshot. Never set by users.
	Offers []protocol.FileOffer `json:"offers,omitempty" toml:"-"`
}

type Env struct {
	OS       string            `json:"os,omitempty" toml:"os"`
	Arch     string            `json:"arch,omitempty" toml:"arch"`
	Python   string            `json:"python,omitempty" toml:"python"`
	Packages map[string]string `json:"packages,omitempty" toml:"packages"`
	GPU      *GPUReq           `json:"gpu,omitempty" toml:"gpu"`
	Probe    ProbeCmd          `json:"probe,omitempty" toml:"probe"`
}

type GPUReq struct {
	VRAMGB   float64 `json:"vram_gb" toml:"vram_gb"`
	Required bool    `json:"required" toml:"required"`
}

// ProbeCmd accepts either a string (tokenised into argv, never run by a
// shell) or an argv array.
type ProbeCmd []string

func (p *ProbeCmd) UnmarshalTOML(v any) error {
	switch x := v.(type) {
	case string:
		argv, err := SplitArgs(x)
		if err != nil {
			return err
		}
		*p = argv
	case []any:
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return errors.New("probe array must contain strings")
			}
			*p = append(*p, s)
		}
	default:
		return errors.New("probe must be a string or an array of strings")
	}
	return nil
}

func (p *ProbeCmd) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		argv, err := SplitArgs(s)
		if err != nil {
			return err
		}
		*p = argv
		return nil
	}
	var a []string
	if err := json.Unmarshal(b, &a); err != nil {
		return errors.New("probe must be a string or an array of strings")
	}
	*p = a
	return nil
}

type Outputs struct {
	Include     []string `json:"include" toml:"include"`
	CopyBack    *bool    `json:"copy_back,omitempty" toml:"copy_back"`
	RetainCapGB float64  `json:"retain_cap_gb" toml:"retain_cap_gb"`
}

type Policy struct {
	OnInterrupt    string     `json:"on_interrupt" toml:"on_interrupt"`
	Checkpoint     Checkpoint `json:"checkpoint" toml:"checkpoint"`
	TimeoutMinutes int        `json:"timeout_minutes" toml:"timeout_minutes"`
}

type Checkpoint struct {
	Pattern string `json:"pattern,omitempty" toml:"pattern"`
}

const (
	OnInterruptFail    = "fail"
	OnInterruptRestart = "restart"
	OnInterruptResume  = "resume-if-checkpoint"
	DefaultMaxTotalGB  = 25
	DefaultRetainCapGB = 10
)

var idRE = regexp.MustCompile(`^tsk_[0-9A-HJKMNP-TV-Z]{26}$`)

// NewID generates a fresh task id.
func NewID() string { return "tsk_" + ulid.Make().String() }

// shellFields are rejected anywhere in a spec (§9.1 absolute rule).
var shellFields = map[string]bool{"shell": true, "sh": true, "bash": true, "cmd": true, "powershell": true,
	"pwsh": true, "script": true, "shell_command": true, "command_line": true, "commandline": true}

func checkShellFields(m map[string]any, where string) error {
	for k, v := range m {
		if shellFields[strings.ToLower(k)] {
			return protocol.Errf(protocol.CodeProtocolError, "spec field %s%s is a shell wrapper; Kith only runs argv arrays", where, k).
				WithFix(`use command = ["prog", "arg1", ...]`)
		}
		if k == "packages" {
			continue
		}
		if sub, ok := v.(map[string]any); ok {
			if err := checkShellFields(sub, where+k+"."); err != nil {
				return err
			}
		}
	}
	if c, ok := m["command"]; ok {
		if _, isStr := c.(string); isStr {
			return protocol.Errf(protocol.CodeProtocolError, "command must be an argv array, not a string").
				WithFix(`use command = ["python", "train.py"]`)
		}
	}
	return nil
}

// ParseTOML parses a task.toml file body ([task] table).
func ParseTOML(b []byte) (*Spec, error) {
	var raw map[string]any
	if _, err := toml.Decode(string(b), &raw); err != nil {
		return nil, protocol.Errf(protocol.CodeProtocolError, "parse task spec: %v", err)
	}
	tbl, ok := raw["task"].(map[string]any)
	if !ok {
		return nil, protocol.Errf(protocol.CodeProtocolError, "task spec needs a [task] table")
	}
	if err := checkShellFields(tbl, "task."); err != nil {
		return nil, err
	}
	var w struct {
		Task Spec `toml:"task"`
	}
	if _, err := toml.Decode(string(b), &w); err != nil {
		return nil, protocol.Errf(protocol.CodeProtocolError, "parse task spec: %v", err)
	}
	s := &w.Task
	return s, s.Normalize()
}

// ParseJSON parses a JSON spec, optionally wrapped as {"task": {...}}.
func ParseJSON(b []byte) (*Spec, error) {
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, protocol.Errf(protocol.CodeProtocolError, "parse task spec: %v", err)
	}
	if inner, ok := raw["task"].(map[string]any); ok && len(raw) == 1 {
		raw = inner
		b, _ = json.Marshal(inner)
	}
	if err := checkShellFields(raw, ""); err != nil {
		return nil, err
	}
	var s Spec
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&s); err != nil {
		return nil, protocol.Errf(protocol.CodeProtocolError, "parse task spec: %v", err)
	}
	return &s, s.Normalize()
}

// Normalize applies defaults and validates. It leaves ID empty if unset.
func (s *Spec) Normalize() error {
	bad := func(f string, a ...any) error { return protocol.Errf(protocol.CodeProtocolError, f, a...) }
	if s.ID != "" && !idRE.MatchString(s.ID) {
		return bad("task id %q must look like tsk_<ULID>", s.ID)
	}
	if len(s.Command) == 0 || s.Command[0] == "" {
		return bad("command must be a non-empty argv array")
	}
	if s.Project == "" {
		s.Project = "default"
	}
	if s.Name == "" {
		s.Name = filepath.Base(s.Command[0])
	}
	if s.Inputs.MaxTotalGB <= 0 {
		s.Inputs.MaxTotalGB = DefaultMaxTotalGB
	}
	for i, f := range s.Inputs.Files {
		c, err := CleanRel(f)
		if err != nil {
			return bad("inputs.files: %v", err)
		}
		s.Inputs.Files[i] = c
	}
	for _, o := range s.Inputs.Offers {
		if _, err := CleanRel(o.Path); err != nil {
			return bad("inputs offer: %v", err)
		}
		if len(o.SHA256) != 64 || o.Size < 0 {
			return bad("inputs offer %s: bad size/hash", o.Path)
		}
	}
	for i, f := range s.Outputs.Include {
		c, err := CleanRel(f)
		if err != nil {
			return bad("outputs.include: %v", err)
		}
		s.Outputs.Include[i] = c
	}
	if s.Outputs.CopyBack == nil {
		t := true
		s.Outputs.CopyBack = &t
	}
	if s.Outputs.RetainCapGB <= 0 {
		s.Outputs.RetainCapGB = DefaultRetainCapGB
	}
	switch s.Policy.OnInterrupt {
	case "":
		s.Policy.OnInterrupt = OnInterruptFail
	case OnInterruptFail, OnInterruptRestart, OnInterruptResume:
	default:
		return bad("policy.on_interrupt must be fail | restart | resume-if-checkpoint")
	}
	if s.Policy.Checkpoint.Pattern != "" {
		c, err := CleanRel(s.Policy.Checkpoint.Pattern)
		if err != nil {
			return bad("policy.checkpoint.pattern: %v", err)
		}
		s.Policy.Checkpoint.Pattern = c
	}
	if s.Policy.TimeoutMinutes < 0 {
		return bad("policy.timeout_minutes must be >= 0")
	}
	if s.Env.GPU != nil && s.Env.GPU.VRAMGB < 0 {
		return bad("env.gpu.vram_gb must be >= 0")
	}
	return nil
}

// JSON marshals the spec for the wire / DB.
func (s *Spec) JSON() []byte {
	b, _ := json.Marshal(s)
	return b
}

// InputsBytes sums the offered input sizes.
func (s *Spec) InputsBytes() int64 {
	var n int64
	for _, o := range s.Inputs.Offers {
		n += o.Size
	}
	return n
}

// SplitArgs tokenises a command string into argv with simple quoting rules
// ('..', "..", backslash escapes inside double quotes). No expansion of any
// kind is performed; the result is executed directly, never by a shell.
func SplitArgs(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inTok := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			inTok = true
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, fmt.Errorf("unterminated ' in %q", s)
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
		case c == '"':
			inTok = true
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\') {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, fmt.Errorf("unterminated \" in %q", s)
			}
		case c == ' ' || c == '\t' || c == '\n':
			if inTok {
				out = append(out, cur.String())
				cur.Reset()
				inTok = false
			}
		default:
			inTok = true
			cur.WriteByte(c)
		}
	}
	if inTok {
		out = append(out, cur.String())
	}
	return out, nil
}
