package spec

import (
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// DefaultSummarizePercent is the share of the context window at which an
// agent compacts its history when the agent does not declare a threshold.
//
// 80% is the usual choice: a request that exactly fills the window is a
// request that gets refused, and the 20% that is left is what the summary
// itself needs.
const DefaultSummarizePercent = 80

// SummarizeAt resolves a compaction threshold against the window of the
// model the agent runs on.
//
// Zero means never. Three things lead there: the agent said so, the window
// is unknown (a percentage of nothing is nothing), or the agent declares
// nothing and there is no default.
//
// The threshold belongs to the agent, the window to the model: this is the
// one place the two meet, so the builder asks here instead of resolving
// either on its own.
func SummarizeAt(t decl.Threshold, window int) int {
	switch {
	case t.Off:
		return 0
	case t.Percent > 0:
		return percentOf(window, t.Percent)
	case t.Tokens > 0:
		return t.Tokens
	default:
		if DefaultSummarizePercent <= 0 {
			return 0
		}
		return percentOf(window, DefaultSummarizePercent)
	}
}

// percentOf turns a share of the window into a token count.
//
// An unknown window yields no threshold: the user has not said how big the
// window is, so there is nothing to take a share of.
func percentOf(window, percent int) int {
	if window <= 0 || percent <= 0 {
		return 0
	}
	return window * percent / 100
}
