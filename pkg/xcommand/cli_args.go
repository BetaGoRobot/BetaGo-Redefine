package xcommand

import (
	"reflect"
	"strings"
)

type cliTagOptions struct {
	Name     string
	Required bool
	Input    bool
	Flag     bool
	Skip     bool
}

func NewTypedCommand[TData, TArgs any](name string, handler CLIArgHandler[TData, TArgs]) *Command[TData] {
	cmd := NewCommand(name, BindCLI(handler))
	return ApplyCLIArgs(cmd, handler)
}

// ApplyCLIArgs attaches help metadata derived from the typed args struct and
// optional handler-provided descriptions/examples at command registration time.
func ApplyCLIArgs[TData, TArgs any](cmd *Command[TData], handler CLIArgHandler[TData, TArgs]) *Command[TData] {
	if cmd == nil || handler == nil {
		return cmd
	}
	if desc := describeCommand(handler); desc != "" {
		cmd.AddDescription(desc)
	}
	cmd.AddExamples(describeCommandExamples(handler)...)
	for _, arg := range describeCLIArgs[TArgs](handler) {
		cmd.AddArgSpec(arg)
	}
	return cmd
}

func describeCLIArgs[TArgs any](handler any) []CommandArg {
	var zero TArgs
	argType := reflect.TypeOf(zero)
	for argType != nil && argType.Kind() == reflect.Pointer {
		argType = argType.Elem()
	}
	if argType == nil || argType.Kind() != reflect.Struct {
		return nil
	}

	toolSpec, hasToolSpec := handler.(interface{ ToolSpec() ToolSpec })
	requiredSet := map[string]struct{}{}
	propDesc := map[string]string{}
	if hasToolSpec {
		spec := toolSpec.ToolSpec()
		if spec.Params != nil {
			for _, name := range spec.Params.Required {
				requiredSet[name] = struct{}{}
			}
			for name, prop := range spec.Params.Props {
				if prop == nil {
					continue
				}
				propDesc[name] = prop.Desc
			}
		}
	}

	args := make([]CommandArg, 0, argType.NumField())
	for i := range argType.NumField() {
		field := argType.Field(i)
		if !field.IsExported() {
			continue
		}

		options := parseCLITag(field.Tag.Get("cli"))
		if options.Skip {
			continue
		}

		jsonName := tagName(field.Tag.Get("json"))
		name := options.Name
		if name == "" {
			name = jsonName
		}
		if name == "" {
			name = strings.ToLower(field.Name)
		}

		desc := field.Tag.Get("help")
		if desc == "" {
			if value := propDesc[name]; value != "" {
				desc = value
			} else if jsonName != "" {
				desc = propDesc[jsonName]
			}
		}

		required := options.Required
		if !required {
			if _, ok := requiredSet[name]; ok {
				required = true
			} else if jsonName != "" {
				_, required = requiredSet[jsonName]
			}
		}

		flag := options.Flag
		if !options.Input && field.Type.Kind() == reflect.Bool {
			flag = true
		}

		args = append(args, CommandArg{
			Name:        name,
			Description: desc,
			Required:    required,
			Input:       options.Input,
			Flag:        flag,
		})
	}

	return args
}

func parseCLITag(tag string) cliTagOptions {
	if tag == "-" {
		return cliTagOptions{Skip: true}
	}
	if tag == "" {
		return cliTagOptions{}
	}

	parts := strings.Split(tag, ",")
	options := cliTagOptions{Name: strings.TrimSpace(parts[0])}
	for _, raw := range parts[1:] {
		switch strings.TrimSpace(raw) {
		case "required":
			options.Required = true
		case "input":
			options.Input = true
		case "flag":
			options.Flag = true
		}
	}
	return options
}

func tagName(tag string) string {
	if tag == "" || tag == "-" {
		return ""
	}
	if idx := strings.IndexByte(tag, ','); idx >= 0 {
		return tag[:idx]
	}
	return tag
}

func describeCommand(handler any) string {
	if handler == nil {
		return ""
	}
	if provider, ok := handler.(interface{ CommandDescription() string }); ok {
		if desc := strings.TrimSpace(provider.CommandDescription()); desc != "" {
			return desc
		}
	}
	if provider, ok := handler.(interface{ ToolSpec() ToolSpec }); ok {
		return strings.TrimSpace(provider.ToolSpec().Desc)
	}
	return ""
}

func describeCommandExamples(handler any) []string {
	if handler == nil {
		return nil
	}
	if provider, ok := handler.(interface{ CommandExamples() []string }); ok {
		return provider.CommandExamples()
	}
	return nil
}
