// Package events implements an independent JSON event stream for the OpenLDAP
// exporter. It runs its own ticker and its own reqStart cursor, so enabling the
// event stream does not perturb Prometheus metric collection, and metric scrapes
// do not drain the event stream before it is observed.
//
// Events are derived exclusively from the slapo-accesslog overlay: every entry
// we were already using to build counters (auditBind, auditAdd, auditModify,
// auditDelete, auditModRDN) is mapped to one or more structured JSON events
// covering the ppolicy and accesslog concerns the exporter can observe
// faithfully. Non-structured sources (slapd syslog text) are intentionally left
// to dedicated log shippers.
package events

import "time"

// Type enumerates the event categories the runner can emit.
// The values are the stable JSON "event" field for each record.
type Type string

const (
	TypeBindSuccess          Type = "bind.success"
	TypeBindFailure          Type = "bind.failure"
	TypeAccountLock          Type = "account.lock"
	TypeAccountUnlock        Type = "account.unlock"
	TypePasswordChange       Type = "password.change"
	TypePasswordResetRequire Type = "password.reset_required"
	TypeWriteAdd             Type = "write.add"
	TypeWriteModify          Type = "write.modify"
	TypeWriteDelete          Type = "write.delete"
	TypeWriteModRDN          Type = "write.modrdn"
)

// AllTypes returns every known event type. Used to validate the filter list
// supplied via OPENLDAP_EVENTS_TYPES and to default-allow everything.
func AllTypes() []Type {
	return []Type{
		TypeBindSuccess,
		TypeBindFailure,
		TypeAccountLock,
		TypeAccountUnlock,
		TypePasswordChange,
		TypePasswordResetRequire,
		TypeWriteAdd,
		TypeWriteModify,
		TypeWriteDelete,
		TypeWriteModRDN,
	}
}

// Event is the serialised record emitted on the events stream. Fields are
// designed to be stable across versions so downstream consumers (Loki, Vector,
// …) can match on them without brittle parsing.
type Event struct {
	Timestamp  time.Time `json:"ts"`
	Event      Type      `json:"event"`
	Server     string    `json:"server"`
	Source     string    `json:"source"`
	ReqStart   string    `json:"req_start,omitempty"`
	ReqSession string    `json:"req_session,omitempty"`
	Actor      string    `json:"actor,omitempty"`
	UserDN     string    `json:"user_dn,omitempty"`
	TargetDN   string    `json:"target_dn,omitempty"`
	Result     string    `json:"result,omitempty"`
	ResultCode string    `json:"result_code,omitempty"`
	Operation  string    `json:"operation,omitempty"`
	Attributes []string  `json:"attributes,omitempty"`
}
