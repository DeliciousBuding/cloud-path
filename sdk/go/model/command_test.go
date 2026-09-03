package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sampleCommand() Command {
	return Command{
		CommandID:      "cmd-1",
		IdempotencyKey: "ik-1",
		EntityID:       "stcb-001/alarm",
		Action:         "calibrate",
		Args:           map[string]any{"offset": 1.5},
		Deadline:       time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC),
		Actor:          "operator",
		Status:         CommandCreated,
		CreatedAt:      time.Date(2026, 9, 3, 8, 59, 0, 0, time.UTC),
	}
}

func TestCommandLifecycle(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		c := sampleCommand()
		want := []CommandStatus{CommandDispatched, CommandAccepted, CommandRunning, CommandSucceeded}
		for _, next := range want {
			if err := c.Transition(next); err != nil {
				t.Fatalf("transition to %s: %v", next, err)
			}
			if c.Status != next {
				t.Fatalf("after transition want %s, got %s", next, c.Status)
			}
		}
		if !c.Status.Terminal() {
			t.Fatalf("SUCCEEDED should be terminal")
		}
	})

	t.Run("skipping stages rejected", func(t *testing.T) {
		c := sampleCommand()
		if err := c.Transition(CommandSucceeded); err == nil {
			t.Fatal("CREATED -> SUCCEEDED must be rejected")
		} else if !strings.Contains(err.Error(), "transition") {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.Status != CommandCreated {
			t.Fatalf("failed transition must not change status, got %s", c.Status)
		}
	})

	t.Run("no-op rejected", func(t *testing.T) {
		c := sampleCommand()
		if err := c.Transition(CommandCreated); err == nil || !strings.Contains(err.Error(), "no-op") {
			t.Fatalf("same-state transition must be rejected, got %v", err)
		}
	})

	t.Run("terminal states", func(t *testing.T) {
		for _, end := range []CommandStatus{CommandFailed, CommandTimedOut, CommandCancelled} {
			c := sampleCommand()
			for _, next := range []CommandStatus{CommandDispatched, CommandAccepted, CommandRunning, end} {
				if err := c.Transition(next); err != nil {
					t.Fatalf("path to %s: %v", end, err)
				}
			}
			if !c.Status.Terminal() {
				t.Fatalf("%s should be terminal", end)
			}
			if err := c.Transition(CommandSucceeded); err == nil {
				t.Fatalf("terminal %s must not transition", end)
			}
		}
	})

	t.Run("invalid next status", func(t *testing.T) {
		c := sampleCommand()
		if err := c.Transition("BOGUS"); err == nil || !strings.Contains(err.Error(), "invalid command status") {
			t.Fatalf("invalid status must be rejected, got %v", err)
		}
		if c.Status != CommandCreated {
			t.Fatalf("failed transition must not change status, got %s", c.Status)
		}
	})

	t.Run("invalid current status", func(t *testing.T) {
		c := sampleCommand()
		c.Status = "BOGUS"
		if err := c.Transition(CommandDispatched); err == nil || !strings.Contains(err.Error(), "no outgoing") {
			t.Fatalf("expected no-outgoing error, got %v", err)
		}
	})

	t.Run("validate", func(t *testing.T) {
		if err := sampleCommand().Validate(); err != nil {
			t.Fatalf("sample command should be valid: %v", err)
		}
		tests := []struct {
			name    string
			mutate  func(*Command)
			wantErr string
		}{
			{"missing command_id", func(c *Command) { c.CommandID = "" }, "command_id"},
			{"missing idempotency_key", func(c *Command) { c.IdempotencyKey = "" }, "idempotency_key"},
			{"missing entity_id", func(c *Command) { c.EntityID = "" }, "entity_id"},
			{"missing action", func(c *Command) { c.Action = "" }, "action"},
			{"missing deadline", func(c *Command) { c.Deadline = time.Time{} }, "deadline"},
			{"missing actor", func(c *Command) { c.Actor = "" }, "actor"},
			{"invalid status", func(c *Command) { c.Status = "BOGUS" }, "status"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				c := sampleCommand()
				tt.mutate(&c)
				err := c.Validate()
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
			})
		}
	})

	t.Run("json roundtrip", func(t *testing.T) {
		c := sampleCommand()
		c.Status = CommandRunning
		c.UpdatedAt = time.Date(2026, 9, 3, 8, 59, 30, 0, time.UTC)
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var got Command
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		b2, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("re-marshal: %v", err)
		}
		if string(b) != string(b2) {
			t.Fatalf("roundtrip not stable:\nwant %s\ngot  %s", b, b2)
		}
		if got.Status != CommandRunning || got.IdempotencyKey != c.IdempotencyKey {
			t.Fatalf("field mismatch: %+v", got)
		}
	})
}
