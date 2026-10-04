package base

import "regexp"

// Well-known event types from ADR 0018 (session/turn/handoff event contract)
// and its FD-42 amendment (task/orchestrator/hosting families).
//
// Producers SHOULD use these names verbatim when the meaning matches.
// The set is extensible: the Session Manager validates shape, not membership.
const (
	// Session lifecycle
	EventSessionCreated  = "session.created"
	EventSessionResumed  = "session.resumed"
	EventSessionArchived = "session.archived"
	EventSessionDeleted  = "session.deleted"

	// Turn
	EventTurnStarted   = "turn.started"
	EventTurnCompleted = "turn.completed"

	// Message
	EventMessageUser      = "message.user"
	EventMessageAssistant = "message.assistant"
	EventMessageSystem    = "message.system"

	// Tool
	EventToolCall   = "tool.call"
	EventToolResult = "tool.result"

	// Handoff (ADR 0005)
	EventHandoffInitiated = "handoff.initiated"
	EventHandoffAccepted  = "handoff.accepted"
	EventHandoffCompleted = "handoff.completed"
	EventHandoffFailed    = "handoff.failed"

	// Memory bank
	EventMemoryBankSynced = "memorybank.synced"

	// Feedback
	EventFeedbackUser       = "feedback.user"
	EventFeedbackSelfCrit   = "feedback.self_critique"

	// Task / orchestrator (FD-42 amendment)
	EventTaskCreated          = "task.created"
	EventTaskStarted          = "task.started"
	EventTaskProgress         = "task.progress"
	EventTaskBlocked          = "task.blocked"
	EventTaskCompleted        = "task.completed"
	EventTaskFailed           = "task.failed"
	EventOrchestratorPlan     = "orchestrator.plan_issued"
	EventOrchestratorStep     = "orchestrator.step_assigned"
	EventOrchestratorAck      = "orchestrator.step_acknowledged"

	// Hosting / conformance (FD-42 amendment)
	EventHostingMode   = "hosting_mode.activated"
	EventHostContract  = "host_contract.issued"
	EventConformance   = "conformance.verified"
)

// eventTypeShape validates the event_type namespace shape required by ADR 0018.
var eventTypeShape = regexp.MustCompile(`^[a-z_]+(\.[a-z_]+)?$`)

// ValidEventType reports whether name matches the required shape.
func ValidEventType(name string) bool {
	return eventTypeShape.MatchString(name)
}