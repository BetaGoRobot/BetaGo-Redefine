// Package xcommand  抽象的Command执行体结构，利用泛型提供多数据类型支持的Command解析、执行流程，约定Root节点为初始节点，不会参与执行匹配
package xcommand

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/logs"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xerror"
	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xhandler"
	"github.com/BetaGoRobot/go_utils/reflecting"
	"go.uber.org/zap"
)

// CommandFunc Repeat
//
//	@author heyuhengmatt
//	@update 2024-07-18 04:43:42
type CommandFunc[T any] func(ctx context.Context, data T, metaData *xhandler.BaseMetaData, args ...string) (err error)

type CommandArgOption struct {
	Value string
	Label string
}

type CommandArg struct {
	Name         string
	Description  string
	Required     bool
	Input        bool
	Flag         bool
	DefaultValue string
	Options      []CommandArgOption
	enumResolver enumDescriptorResolver
}

// Command Repeat
//
//	@author heyuhengmatt
//	@update 2024-07-18 04:43:37
type Command[T any] struct {
	Name              string
	Aliases           []string
	Description       string
	Examples          []string
	SubCommands       map[string]*Command[T]
	DefaultSubCommand string
	Func              CommandFunc[T]
	Usage             string
	SupportArgs       map[string]*CommandArg
	supportArgSeq     []string
	curComChain       []string
}

// Execute 从当前节点开始，执行Command
//
//	@param c *Command[T]
//	@return Execute
//	@author heyuhengmatt
//	@update 2024-07-18 05:30:21
func (c *Command[T]) Execute(ctx context.Context, data T, metaData *xhandler.BaseMetaData, args []string) error {
	l := logs.L().Ctx(ctx).With(zap.String("command_name", c.Name), zap.Strings("args", args), zap.Any("meta", metaData))
	l.Debug("Executing On Command")
	if c.Func != nil { // 当前Command有执行方法，直接执行
		l.Info("Executing on Command Function", zap.String("func_name", reflecting.GetFunctionName(c.Func)))
		return c.Func(ctx, data, metaData, args...)
	}
	if len(args) == 0 { // 无执行方法且无后续参数
		if subcommand, ok := c.defaultSubCommand(); ok {
			return subcommand.Execute(ctx, data, metaData, nil)
		}
		return fmt.Errorf("%w: %s", xerror.ErrCommandIncomplete, c.FormatUsage())
	}
	if helpArgs, ok := c.redirectHelpArgs(args); ok {
		helpCommand := c.SubCommands["help"]
		if helpCommand != nil {
			return helpCommand.Execute(ctx, data, metaData, helpArgs)
		}
	}

	if subcommand, ok := c.lookupSubCommand(args[0]); ok {
		err := subcommand.Execute(ctx, data, metaData, args[1:])
		if err != nil && err == xerror.ErrArgsIncompelete {
			return fmt.Errorf("%w: %s", xerror.ErrArgsIncompelete, subcommand.FormatUsage())
		}
		return err
	}

	return fmt.Errorf(
		"%w: Command <b>%s</b> not found, available sub-commands: %s",
		xerror.ErrCommandNotFound,
		args[0],
		fmt.Sprintf(" [%s]", strings.Join(c.GetSubCommands(), ", ")),
	)
}

// BuildChain 从当前节点开始，执行Command
//
//	@param c *Command[T]
//	@return Execute
//	@author heyuhengmatt
//	@update 2024-07-18 05:30:21
func (c *Command[T]) BuildChain() {
	for _, subcommand := range c.SubCommands {
		subcommand.curComChain = append(c.curComChain, subcommand.Name)
		subcommand.BuildChain()
	}
}

// FormatUsage 获取当前节点的所有SubCommands
//
//	@param c
//	@return GetSubCommands
func (c *Command[T]) FormatUsage() string {
	if c.Usage == "" {
		baseUsage := c.Path()
		if len(c.SupportArgs) != 0 {
			baseUsage += " " + strings.Join(c.GetSupportArgs(), " ")
		}
		if len(c.SubCommands) != 0 {
			baseUsage += fmt.Sprintf(" [%s]", strings.Join(c.GetSubCommands(), ", "))
		}
		if details := c.FormatArgDetails(); details != "" {
			baseUsage += "\n" + details
		}

		return baseUsage
	}
	return c.Usage
}

// GetSubCommands 获取当前节点的所有SubCommands
//
//	@param c
//	@return GetSubCommands
func (c *Command[T]) GetSubCommands() []string {
	availableComs := make([]string, 0, len(c.SubCommands))
	for k := range c.SubCommands {
		availableComs = append(availableComs, k)
	}
	sort.Strings(availableComs)
	return availableComs
}

// GetSupportArgs 获取当前节点的所有SubCommands
//
//	@param c
//	@return GetSubCommands
func (c *Command[T]) GetSupportArgs() []string {
	supportArgs := make([]string, 0, len(c.supportArgSeq))
	for _, spec := range c.GetArgSpecs() {
		supportArgs = append(supportArgs, spec.UsageToken())
	}
	return supportArgs
}

func (c *Command[T]) GetArgSpecs() []CommandArg {
	argSpecs := make([]CommandArg, 0, len(c.supportArgSeq))
	for _, name := range c.supportArgSeq {
		spec := c.SupportArgs[name]
		if spec == nil {
			continue
		}
		argSpecs = append(argSpecs, spec.resolved())
	}
	return argSpecs
}

func (c *Command[T]) FormatArgDetails() string {
	if len(c.supportArgSeq) == 0 {
		return ""
	}
	lines := make([]string, 0, len(c.supportArgSeq)+1)
	lines = append(lines, "Args:")
	for _, name := range c.supportArgSeq {
		spec := c.SupportArgs[name]
		if spec == nil {
			continue
		}
		line := "  " + spec.UsageToken()
		if spec.Description != "" {
			line += ": " + spec.Description
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// redirectHelpArgs routes inline `--help` requests to the explicit `/help` command
// so both entry points share the same handler and rendering logic.
func (c *Command[T]) redirectHelpArgs(args []string) ([]string, bool) {
	if c.Path() != "/" {
		return nil, false
	}
	if len(args) == 0 || args[0] == "help" {
		return nil, false
	}
	if _, ok := c.SubCommands["help"]; !ok {
		return nil, false
	}

	path := make([]string, 0, len(args))
	for _, token := range args {
		if token == "--help" {
			return path, true
		}
		if strings.HasPrefix(token, "--") {
			continue
		}
		path = append(path, token)
	}
	return nil, false
}

// Validate 从当前节点开始，执行Command
//
//	@param c *Command[T]
//	@return Execute
//	@author heyuhengmatt
//	@update 2024-07-18 05:30:21
func (c *Command[T]) Validate(ctx context.Context, data T, args []string) bool {
	if c.Func != nil { // 当前Command有执行方法，直接执行
		return true
	}
	if len(args) == 0 { // 无执行方法且无后续参数
		if subcommand, ok := c.defaultSubCommand(); ok {
			return subcommand.Validate(ctx, data, nil)
		}
		return false
	}
	if subcommand, ok := c.lookupSubCommand(args[0]); ok {
		return subcommand.Validate(ctx, data, args[1:])
	}
	return true
}

// AddSubCommand 添加一个SubCommand
//
//	@param c *Command[T]
//	@return AddSubCommand
//	@author heyuhengmatt
//	@update 2024-07-18 05:30:07
func (c *Command[T]) AddSubCommand(subCommand *Command[T]) *Command[T] {
	if c == nil || subCommand == nil {
		return c
	}
	subCommand.Name = strings.TrimSpace(subCommand.Name)
	if subCommand.Name == "" {
		return c
	}
	if _, ok := c.lookupSubCommand(subCommand.Name); ok {
		panic("duplicate subcommand: " + subCommand.Name)
	}
	for _, alias := range subCommand.GetAliases() {
		if _, ok := c.lookupSubCommand(alias); ok {
			panic("duplicate subcommand alias: " + alias)
		}
	}
	c.SubCommands[subCommand.Name] = subCommand
	return c
}

func (c *Command[T]) AddAliases(aliases ...string) *Command[T] {
	if c == nil {
		return c
	}
	seen := make(map[string]struct{}, len(c.Aliases)+len(aliases)+1)
	seen[c.Name] = struct{}{}
	next := make([]string, 0, len(c.Aliases)+len(aliases))
	for _, alias := range append(append([]string{}, c.Aliases...), aliases...) {
		alias = strings.TrimSpace(strings.TrimPrefix(alias, "/"))
		if alias == "" {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		next = append(next, alias)
	}
	c.Aliases = next
	return c
}

func (c *Command[T]) GetAliases() []string {
	if c == nil || len(c.Aliases) == 0 {
		return nil
	}
	result := make([]string, 0, len(c.Aliases))
	seen := make(map[string]struct{}, len(c.Aliases)+1)
	if name := strings.TrimSpace(c.Name); name != "" {
		seen[name] = struct{}{}
	}
	for _, alias := range c.Aliases {
		alias = strings.TrimSpace(strings.TrimPrefix(alias, "/"))
		if alias == "" {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		result = append(result, alias)
	}
	return result
}

func (c *Command[T]) SetDefaultSubCommand(name string) *Command[T] {
	c.DefaultSubCommand = strings.TrimSpace(name)
	return c
}

// AddUsage 添加一个SubCommand
//
//	@param c *Command[T]
//	@return AddUsage
func (c *Command[T]) AddUsage(usage string) *Command[T] {
	c.Usage = usage
	return c
}

func (c *Command[T]) AddDescription(description string) *Command[T] {
	c.Description = description
	return c
}

func (c *Command[T]) AddExamples(examples ...string) *Command[T] {
	for _, example := range examples {
		example = strings.TrimSpace(example)
		if example == "" {
			continue
		}
		c.Examples = append(c.Examples, example)
	}
	return c
}

func (c *Command[T]) GetExamples() []string {
	if len(c.Examples) == 0 {
		return nil
	}
	res := make([]string, 0, len(c.Examples))
	seen := make(map[string]struct{}, len(c.Examples))
	for _, example := range c.Examples {
		example = strings.TrimSpace(example)
		if example == "" {
			continue
		}
		if _, ok := seen[example]; ok {
			continue
		}
		seen[example] = struct{}{}
		res = append(res, example)
	}
	return res
}

func (c *Command[T]) defaultSubCommand() (*Command[T], bool) {
	if c == nil || c.DefaultSubCommand == "" || len(c.SubCommands) == 0 {
		return nil, false
	}
	subcommand, ok := c.SubCommands[c.DefaultSubCommand]
	if !ok || subcommand == nil {
		return nil, false
	}
	return subcommand, true
}

// AddArgs 添加一个SubCommand
//
//	@param c *Command[T]
//	@return AddUsage
func (c *Command[T]) AddArgs(args ...string) *Command[T] {
	for _, arg := range args {
		c.AddArgSpec(CommandArg{Name: arg})
	}
	return c
}

func (c *Command[T]) AddArgSpec(arg CommandArg) *Command[T] {
	if arg.Name == "" {
		return c
	}
	if _, ok := c.SupportArgs[arg.Name]; !ok {
		c.supportArgSeq = append(c.supportArgSeq, arg.Name)
	}
	spec := arg
	if !spec.Input && spec.Name != "" && !spec.Flag {
		spec.Flag = false
	}
	c.SupportArgs[arg.Name] = &spec
	return c
}

func (a CommandArg) resolved() CommandArg {
	if a.enumResolver == nil {
		return a
	}
	desc := sanitizeEnumDescriptor(a.enumResolver())
	a.Options = desc.Options
	a.DefaultValue = desc.DefaultValue
	return a
}

// IsCommand 判断传入的文本是否符合 Command 的触发格式
//
//	@param text string 原始文本，如 "/abc d --ef=1"
//	@return bool
//	@author heyuhengmatt
//	@update 2024-07-18 05:40:00
func (c *Command[T]) IsCommand(ctx context.Context, text string) bool {
	cmds := GetCommand(ctx, text)
	if len(cmds) == 0 {
		return false
	}

	targetName := cmds[0]
	if _, ok := c.lookupSubCommand(targetName); ok {
		return true
	}
	if c.matchesName(targetName) {
		return true
	}

	return false
}

// NewCommand 创建一个新的Command结构
//
//	@param name string
//	@param fn CommandFunc[T]
//	@return *Command
//	@author heyuhengmatt
//	@update 2024-07-18 05:29:58
func NewCommand[T any](name string, fn CommandFunc[T]) *Command[T] {
	return &Command[T]{
		Name:        name,
		SubCommands: make(map[string]*Command[T]),
		Func:        fn,
		SupportArgs: make(map[string]*CommandArg),
	}
}

// NewCommand 创建一个新的Command结构
//
//	@param name string
//	@param fn CommandFunc[T]
//	@return *Command
//	@author heyuhengmatt
//	@update 2024-07-18 05:29:58
func NewRootCommand[T any](fn CommandFunc[T]) *Command[T] {
	return &Command[T]{
		Name:        "root",
		SubCommands: make(map[string]*Command[T]),
		Func:        fn,
		SupportArgs: make(map[string]*CommandArg),
	}
}

func (a CommandArg) UsageToken() string {
	switch {
	case a.Input && a.Required:
		return "<" + a.Name + ">"
	case a.Input:
		return "[" + a.Name + "]"
	case a.Flag && a.Required:
		return "--" + a.Name
	case a.Flag:
		return "[--" + a.Name + "]"
	case a.Required:
		return "--" + a.Name + "=<value>"
	default:
		return "[--" + a.Name + "=<value>]"
	}
}

func (c *Command[T]) Path() string {
	if len(c.curComChain) != 0 {
		return "/" + strings.Join(c.curComChain, " ")
	}
	if c.Name == "" || c.Name == "root" {
		return "/"
	}
	return "/" + c.Name
}

func (c *Command[T]) Find(path ...string) *Command[T] {
	cur := c
	for _, item := range path {
		name := strings.TrimSpace(strings.TrimPrefix(item, "/"))
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "--") {
			return nil
		}
		next, ok := cur.lookupSubCommand(name)
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

func (c *Command[T]) LookupSubCommand(name string) (*Command[T], bool) {
	return c.lookupSubCommand(name)
}

func (c *Command[T]) lookupSubCommand(name string) (*Command[T], bool) {
	if c == nil {
		return nil, false
	}
	name = strings.TrimSpace(strings.TrimPrefix(name, "/"))
	if name == "" {
		return nil, false
	}
	if next, ok := c.SubCommands[name]; ok && next != nil {
		return next, true
	}
	for _, subcommand := range c.SubCommands {
		if subcommand == nil {
			continue
		}
		if subcommand.matchesName(name) {
			return subcommand, true
		}
	}
	return nil, false
}

func (c *Command[T]) matchesName(name string) bool {
	if c == nil {
		return false
	}
	name = strings.TrimSpace(strings.TrimPrefix(name, "/"))
	if name == "" {
		return false
	}
	if strings.TrimSpace(c.Name) == name {
		return true
	}
	for _, alias := range c.GetAliases() {
		if alias == name {
			return true
		}
	}
	return false
}
