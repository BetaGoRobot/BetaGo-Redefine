package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
)

type CommandInvocation struct {
	CommandName string   `json:"command_name"`
	RawCommand  string   `json:"raw_command"`
	ParsedArgs  []string `json:"parsed_args"`
}

type CommandBridgeExecutor func(context.Context, CommandInvocation, CapabilityRequest) (CapabilityResult, error)

type CommandBridgeCapability struct {
	meta        CapabilityMeta
	commandName string
	executor    CommandBridgeExecutor
}

type commandBridgePayload struct {
	RawCommand string   `json:"raw_command,omitempty"`
	Args       []string `json:"args,omitempty"`
}

func NewCommandBridgeCapability(commandName string, meta CapabilityMeta, executor CommandBridgeExecutor) Capability {
	if meta.Name == "" {
		meta.Name = commandName
	}
	if meta.Kind == "" {
		meta.Kind = CapabilityKindCommand
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

func (c *CommandBridgeCapability) Meta() CapabilityMeta {
	return c.meta
}

func (c *CommandBridgeCapability) Execute(ctx context.Context, req CapabilityRequest) (CapabilityResult, error) {
	if c == nil || c.executor == nil {
		return CapabilityResult{}, fmt.Errorf("command bridge executor is not initialized")
	}

	invocation, err := c.decodeInvocation(ctx, req)
	if err != nil {
		return CapabilityResult{}, err
	}
	return c.executor(ctx, invocation, req)
}

func (c *CommandBridgeCapability) decodeInvocation(ctx context.Context, req CapabilityRequest) (CommandInvocation, error) {
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

func (c *CommandBridgeCapability) resolveRawCommand(req CapabilityRequest) (string, error) {
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

func BuildDefaultCommandBridgeCapabilities() []Capability {
	return []Capability{
		NewCommandBridgeCapability(
			"bb",
			CapabilityMeta{
				Name:              "bb",
				Kind:              CapabilityKindCommand,
				Description:       "bridge the /bb chat entry into agent runtime",
				SideEffectLevel:   SideEffectLevelChatWrite,
				SupportsStreaming: true,
				AllowedScopes:     []CapabilityScope{CapabilityScopeGroup, CapabilityScopeP2P},
				DefaultTimeout:    time.Minute,
			},
			func(context.Context, CommandInvocation, CapabilityRequest) (CapabilityResult, error) {
				return CapabilityResult{}, fmt.Errorf("command bridge executor is not configured")
			},
		),
	}
}
