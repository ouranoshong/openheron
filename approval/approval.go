// Package approval carries the human decision a gated tool reads back when it
// resumes.
package approval

import "encoding/gob"

type Decision struct {
	Approved bool   `json:"approved"`
	Reason   string `json:"reason,omitempty"`
}

type ApprovalInfo struct {
	ToolName        string
	ArgumentsInJSON string
}

func init() {
	gob.Register(&Decision{})
	gob.Register(&ApprovalInfo{})
}
