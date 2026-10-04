// Package metrics provides Prometheus-compatible counters for the GSM.
package metrics

import "sync/atomic"

// Metrics holds the GSM's observable counters.
type Metrics struct {
	SessionsCreated  atomic.Int64
	EventsAppended   atomic.Int64
	PublicationsRecv atomic.Int64
	PublicationsRej  atomic.Int64
	FanoutCalls      atomic.Int64
	FanoutErrors     atomic.Int64
	RejectedInbound  atomic.Int64
}

// Global is the singleton metrics instance.
var Global = &Metrics{}