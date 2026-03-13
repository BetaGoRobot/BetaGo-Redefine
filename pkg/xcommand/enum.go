package xcommand

import (
	"fmt"
	"reflect"
	"strings"
)

type EnumDescriptor struct {
	Options      []CommandArgOption
	DefaultValue string
}

type enumDescriptorResolver func() EnumDescriptor

type enumDescriber interface {
	CommandEnum() EnumDescriptor
}

func (d EnumDescriptor) Normalize(raw string) (string, error) {
	desc := sanitizeEnumDescriptor(d)
	value := strings.TrimSpace(raw)
	if value == "" {
		return desc.DefaultValue, nil
	}

	if len(desc.Options) == 0 {
		return value, nil
	}

	for _, option := range desc.Options {
		if option.Value == value {
			return value, nil
		}
	}

	choices := make([]string, 0, len(desc.Options))
	for _, option := range desc.Options {
		choices = append(choices, option.Value)
	}
	return "", fmt.Errorf("invalid value %q, available: %s", value, strings.Join(choices, ", "))
}

func ParseEnum[T ~string](raw string) (T, error) {
	var zero T
	provider, ok := any(zero).(enumDescriber)
	if !ok {
		return zero, fmt.Errorf("type %T does not implement CommandEnum", zero)
	}
	normalized, err := provider.CommandEnum().Normalize(raw)
	if err != nil {
		return zero, err
	}
	return T(normalized), nil
}

func boolEnumDescriptor() EnumDescriptor {
	return EnumDescriptor{
		Options: []CommandArgOption{
			{Value: "true", Label: "是"},
			{Value: "false", Label: "否"},
		},
		DefaultValue: "false",
	}
}

func enumDescriptorFromType(t reflect.Type) (EnumDescriptor, bool) {
	resolver, ok := enumDescriptorResolverFromType(t)
	if !ok {
		return EnumDescriptor{}, false
	}
	return sanitizeEnumDescriptor(resolver()), true
}

func enumDescriptorResolverFromType(t reflect.Type) (enumDescriptorResolver, bool) {
	baseType := baseFieldType(t)
	if baseType == nil {
		return nil, false
	}
	if baseType.Kind() == reflect.Bool {
		return boolEnumDescriptor, true
	}
	value := reflect.New(baseType).Elem().Interface()
	if _, ok := value.(enumDescriber); !ok {
		return nil, false
	}
	return func() EnumDescriptor {
		value := reflect.New(baseType).Elem().Interface()
		provider, ok := value.(enumDescriber)
		if !ok {
			return EnumDescriptor{}
		}
		return provider.CommandEnum()
	}, true
}

func sanitizeEnumDescriptor(desc EnumDescriptor) EnumDescriptor {
	desc.DefaultValue = strings.TrimSpace(desc.DefaultValue)
	if len(desc.Options) == 0 {
		return desc
	}

	options := make([]CommandArgOption, 0, len(desc.Options))
	seen := make(map[string]struct{}, len(desc.Options))
	for _, option := range desc.Options {
		value := strings.TrimSpace(option.Value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		label := strings.TrimSpace(option.Label)
		if label == "" {
			label = value
		}
		options = append(options, CommandArgOption{
			Value: value,
			Label: label,
		})
		seen[value] = struct{}{}
	}
	desc.Options = options
	return desc
}
