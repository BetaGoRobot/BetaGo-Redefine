package capability

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
)

// CommandInvocation carries capability runtime state.
type CommandInvocation struct {
	CommandName string   `json:"command_name"`
	RawCommand  string   `json:"raw_command"`
	ParsedArgs  []string `json:"parsed_args"`
}

// CommandBridgeExecutor names a capability runtime type.
type CommandBridgeExecutor func(context.Context, CommandInvocation, Request) (Result, error)

// CommandBridgeCapability carries capability runtime state.
type CommandBridgeCapability struct {
	meta        Meta
	commandName string
	executor    CommandBridgeExecutor
}

type commandBridgePayload struct {
	RawCommand string   `json:"raw_command,omitempty"`
	Args       []string `json:"args,omitempty"`
}

// NewCommandBridgeCapability implements capability runtime behavior.
func NewCommandBridgeCapability(commandName string, meta Meta, executor CommandBridgeExecutor) Capability {
	if meta.Name == "" {
		meta.Name = commandName
	}
	if meta.Kind == "" {
		meta.Kind = KindCommand
	}
	if meta.DefaultTimeout <= 0 {
		meta.DefaultTimeout = time.Minute
	}
	return &CommandBridgeCapability{
		meta:        meta,
		commandName: commandName,
		executor:    executor,
	}
}

// Meta implements capability runtime behavior.
func (c *CommandBridgeCapability) Meta() Meta {
	return c.meta
}

// Execute implements capability runtime behavior.
func (c *CommandBridgeCapability) Execute(ctx context.Context, req Request) (Result, error) {
	if c == nil || c.executor == nil {
		return Result{}, fmt.Errorf("command bridge executor is not initialized")
	}

	invocation, err := c.decodeInvocation(ctx, req)
	if err != nil {
		return Result{}, err
	}
	return c.executor(ctx, invocation, req)
}

func (c *CommandBridgeCapability) decodeInvocation(ctx context.Context, req Request) (CommandInvocation, error) {
	rawCommand, err := c.resolveRawCommand(req)
	if err != nil {
		return CommandInvocation{}, err
	}
	parsed := xcommand.GetCommand(ctx, rawCommand)
	if len(parsed) == 0 {
		return CommandInvocation{}, fmt.Errorf("invalid command bridge input: %s", rawCommand)
	}

	return CommandInvocation{
		CommandName: parsed[0],
		RawCommand:  rawCommand,
		ParsedArgs:  parsed,
	}, nil
}

func (c *CommandBridgeCapability) resolveRawCommand(req Request) (string, error) {
	payload := commandBridgePayload{}
	if raw := strings.TrimSpace(string(req.PayloadJSON)); raw != "" {
		if err := json.Unmarshal(req.PayloadJSON, &payload); err != nil {
			return "", err
		}
	}

	switch {
	case strings.TrimSpace(payload.RawCommand) != "":
		return strings.TrimSpace(payload.RawCommand), nil
	case len(payload.Args) > 0:
		parts := append([]string{"/" + c.commandName}, payload.Args...)
		return strings.Join(parts, " "), nil
	case strings.TrimSpace(req.InputText) != "":
		return "/" + c.commandName + " " + strings.TrimSpace(req.InputText), nil
	default:
		return "/" + c.commandName, nil
	}
}

// BuildDefaultCommandBridgeCapabilities implements capability runtime behavior.
func BuildDefaultCommandBridgeCapabilities() []Capability {
	return []Capability{
		NewCommandBridgeCapability(
			"bb",
			Meta{
				Name:              "bb",
				Kind:              KindCommand,
				Description:       "bridge the /bb chat entry into agent runtime",
				SideEffectLevel:   SideEffectLevelChatWrite,
				SupportsStreaming: true,
				AllowedScopes:     []Scope{ScopeGroup, ScopeP2P},
				DefaultTimeout:    time.Minute,
			},
			func(context.Context, CommandInvocation, Request) (Result, error) {
				return Result{}, fmt.Errorf("command bridge executor is not configured")
			},
		),
	}
}
