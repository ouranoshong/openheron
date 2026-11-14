package run

import (
	"sync"

	"github.com/ouranoshong/openheron/contract"
)

// sessionAnnouncer is the session's announcement channel.
//
// ★ A run's bus dies with the run. The approval does not: the run that
//
//	raised it ends, and the approval is still waiting for an answer. So the
//	fact "the approval was answered" has nowhere to go on that run's bus —
//	and a surface that is waiting on the approval sits on a question that
//	no longer exists, offering answers the server refuses.
//
// ★ The session's channel is that somewhere. It outlives every run on the
//
//	session, and it reaches the surfaces that are not watching the run that
//	raised the approval.
//
// ★ The facts are **live notifications**. The past is the scan's job: the
//
//	ledger holds what happened, and a surface that was not listening
//	hydrates from there.
type sessionAnnouncer struct {
	mu    sync.Mutex
	buses map[string]*publisher
}

func newSessionAnnouncer() *sessionAnnouncer {
	return &sessionAnnouncer{buses: map[string]*publisher{}}
}

// bus returns the session's bus, creating it when there is none.
//
// ★ It is created by a **subscriber**, never by a publisher: a fact nobody
//
//	is listening for has no one to deliver it to, and a bus kept for it
//	would be a leak — one entry per session the process ever saw.
func (a *sessionAnnouncer) bus(sessionID string) *publisher {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.buses[sessionID]
	if p == nil {
		p = newLivePublisher()
		a.buses[sessionID] = p
	}
	return p
}

// subscribe is a view of the session's announcements.
func (a *sessionAnnouncer) subscribe(sessionID string) *subscription {
	return a.bus(sessionID).Subscribe()
}

// release drops the session's bus once nobody is listening any more.
func (a *sessionAnnouncer) release(sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.buses[sessionID]
	if p == nil {
		return
	}
	if p.empty() {
		delete(a.buses, sessionID)
	}
}

// publish hands one fact to the session's subscribers.
//
// ★ A session with no subscribers gets nothing: the fact is a
//
//	notification, and the durable truth is the ledger — a surface that was
//	not listening learns it from the scan.
func (a *sessionAnnouncer) publish(sessionID string, ev contract.RunEvent) {
	a.mu.Lock()
	p := a.buses[sessionID]
	a.mu.Unlock()
	if p == nil {
		return
	}
	p.Publish(ev)
}

// Announce is a view of the session's announcements.
//
// ★ It is the session's channel, not a run's: it outlives the runs, so a
//
//	surface that is waiting for an approval hears that it was answered
//	even though the run that raised it is over.
func (rt *Runtime) Announce(sessionID string) *subscription {
	return rt.announcer.subscribe(sessionID)
}

// ReleaseAnnounce lets go of the session's channel.
//
// ★ The bus is dropped with its last subscriber: a session nobody watches
//
//	keeps no bus, or the map grows with every session the process ever saw.
func (rt *Runtime) ReleaseAnnounce(sessionID string) {
	rt.announcer.release(sessionID)
}

// AnnounceWatchers reports how many surfaces listen to the session's
// announcements.
//
// ★ It is a read-only query, and it exists to tell "nobody is listening"
//
//	apart from "the listener is not there **yet**". ★ The distinction
//	matters because the facts are notifications: one published before the
//	listener arrived is gone for good, and a caller that must not miss one
//	has to be able to ask.
func (rt *Runtime) AnnounceWatchers(sessionID string) int {
	rt.announcer.mu.Lock()
	p := rt.announcer.buses[sessionID]
	rt.announcer.mu.Unlock()
	if p == nil {
		return 0
	}
	return p.watcherCount()
}

// announceApprovalResolved says the approvals were answered.
//
// ★ The approval belongs to the session, and the run that consumed it is a
//
//	**different** run. So this fact cannot ride the run's bus: the run that
//	raised the approval is over by the time anybody can answer it.
//
// ★ One fact per approval: the surface that shows a card drops exactly the
//
//	one that was answered, and a card with several approvals in it is
//	answered one by one.
//
// ★ The answering run is named, so a surface that was waiting learns where
//
//	the round went — but it is not told the round started. The two are
//	separate facts, and a resume that consumed the approval and then found
//	nothing to answer ends the round without starting one.
func (rt *Runtime) announceApprovalResolved(sessionID string, pending []contract.PendingInterrupt,
	decisions []ApprovalDecision, runID string) {
	for _, p := range pending {
		ev := contract.RunEvent{
			Kind:        contract.RunEventApprovalResolved,
			RunID:       runID,
			InterruptID: p.InterruptID,
			Approved:    decisionFor(p.InterruptID, decisions),
		}
		if reason := reasonFor(p.InterruptID, decisions); reason != "" {
			ev.Reason = reason
		}
		rt.announcer.publish(sessionID, ev)
	}
}

// announceRunStarted says a new run owns the session.
//
// ★ It is what lets a surface that missed the start follow the round: the
//
//	run's own stream belongs to the surface that asked, and a surface that
//	was not there when the round began has no stream at all.
func (rt *Runtime) announceRunStarted(sessionID, runID string, kind contract.TurnKind) {
	rt.announcer.publish(sessionID, contract.RunEvent{
		Kind:     contract.RunEventRunStarted,
		RunID:    runID,
		TurnKind: kind,
	})
}

// decisionFor reports whether the approval was approved.
func decisionFor(interruptID string, decisions []ApprovalDecision) bool {
	for _, d := range decisions {
		if d.InterruptID == interruptID {
			return d.Approved
		}
	}
	return false
}

// reasonFor reports the reason the user gave, if any.
func reasonFor(interruptID string, decisions []ApprovalDecision) string {
	for _, d := range decisions {
		if d.InterruptID == interruptID {
			return d.Reason
		}
	}
	return ""
}
