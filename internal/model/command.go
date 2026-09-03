package model

import (
	"errors"
	"fmt"
	"time"
)

// CommandStatus 是 Command 生命周期状态。
//
// 状态机（docs/architecture/capability-model.md §4）：
//
//	CREATED → DISPATCHED → ACCEPTED → RUNNING
//	                                └→ SUCCEEDED / FAILED / TIMED_OUT / CANCELLED
type CommandStatus string

const (
	CommandCreated    CommandStatus = "CREATED"
	CommandDispatched CommandStatus = "DISPATCHED"
	CommandAccepted   CommandStatus = "ACCEPTED"
	CommandRunning    CommandStatus = "RUNNING"
	CommandSucceeded  CommandStatus = "SUCCEEDED"
	CommandFailed     CommandStatus = "FAILED"
	CommandTimedOut   CommandStatus = "TIMED_OUT"
	CommandCancelled  CommandStatus = "CANCELLED"
)

// Valid 报告 s 是否为合法状态。
func (s CommandStatus) Valid() bool {
	switch s {
	case CommandCreated, CommandDispatched, CommandAccepted, CommandRunning,
		CommandSucceeded, CommandFailed, CommandTimedOut, CommandCancelled:
		return true
	}
	return false
}

// Terminal 报告 s 是否为终态。
func (s CommandStatus) Terminal() bool {
	switch s {
	case CommandSucceeded, CommandFailed, CommandTimedOut, CommandCancelled:
		return true
	}
	return false
}

// transitions 是严格按契约状态机的相邻转移表。终态无出边。
var transitions = map[CommandStatus][]CommandStatus{
	CommandCreated:    {CommandDispatched},
	CommandDispatched: {CommandAccepted},
	CommandAccepted:   {CommandRunning},
	CommandRunning:    {CommandSucceeded, CommandFailed, CommandTimedOut, CommandCancelled},
}

// Command 是有生命周期、幂等键和超时语义的动作请求。
//
// action 指向 Capability 声明的 Action；args 是 JSON 对象参数。
// idempotency_key 保证同一逻辑命令重复下发只执行一次。
type Command struct {
	CommandID      string         `json:"command_id"`
	IdempotencyKey string         `json:"idempotency_key"`
	EntityID       string         `json:"entity_id"`
	Action         string         `json:"action"`
	Args           map[string]any `json:"args,omitempty"`
	Deadline       time.Time      `json:"deadline"`
	Actor          string         `json:"actor"`
	Status         CommandStatus  `json:"status"`
	Result         any            `json:"result,omitempty"`
	Error          string         `json:"error,omitempty"`
	CreatedAt      time.Time      `json:"created_at,omitempty"`
	UpdatedAt      time.Time      `json:"updated_at,omitempty"`
}

// NewCommand 构造一条 CREATED 状态的 Command。
func NewCommand(commandID, idempotencyKey, entityID, action string, args map[string]any, deadline time.Time, actor string) Command {
	return Command{
		CommandID:      commandID,
		IdempotencyKey: idempotencyKey,
		EntityID:       entityID,
		Action:         action,
		Args:           args,
		Deadline:       deadline,
		Actor:          actor,
		Status:         CommandCreated,
		CreatedAt:      time.Now(),
	}
}

// Transition 把命令推进到 next。只有契约状态机允许的相邻转移才会成功；
// 非法状态、同态空转与从终态出发都会返回错误且不修改 Status。
func (c *Command) Transition(next CommandStatus) error {
	if !next.Valid() {
		return fmt.Errorf("model: command %q: invalid command status %q", c.CommandID, next)
	}
	if c.Status == next {
		return fmt.Errorf("model: command %q: no-op transition %s -> %s", c.CommandID, c.Status, next)
	}
	allowed := transitions[c.Status]
	if len(allowed) == 0 {
		return fmt.Errorf("model: command %q: status %s has no outgoing transitions", c.CommandID, c.Status)
	}
	for _, s := range allowed {
		if s == next {
			c.Status = next
			return nil
		}
	}
	return fmt.Errorf("model: command %q: invalid transition %s -> %s", c.CommandID, c.Status, next)
}

// Validate 校验 Command：必需字段非空、deadline 存在、状态合法。
func (c Command) Validate() error {
	var errs []error
	if c.CommandID == "" {
		errs = append(errs, fieldErrorf("command", "command_id", "required and must not be empty"))
	}
	if c.IdempotencyKey == "" {
		errs = append(errs, fieldErrorf("command", "idempotency_key", "required and must not be empty"))
	}
	if c.EntityID == "" {
		errs = append(errs, fieldErrorf("command", "entity_id", "required and must not be empty"))
	}
	if c.Action == "" {
		errs = append(errs, fieldErrorf("command", "action", "required and must not be empty"))
	}
	if c.Deadline.IsZero() {
		errs = append(errs, fieldErrorf("command", "deadline", "required"))
	}
	if c.Actor == "" {
		errs = append(errs, fieldErrorf("command", "actor", "required and must not be empty"))
	}
	if !c.Status.Valid() {
		errs = append(errs, fieldErrorf("command", "status", "invalid command status %q", c.Status))
	}
	return errors.Join(errs...)
}
