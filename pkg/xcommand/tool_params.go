package xcommand

import (
	"reflect"
	"strings"

	arktools "github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
)

func enrichToolParamsFromArgs[TArgs any](params *arktools.Param) *arktools.Param {
	if params == nil || len(params.Props) == 0 {
		return params
	}

	argType := reflect.TypeOf((*TArgs)(nil)).Elem()
	for argType != nil && argType.Kind() == reflect.Pointer {
		argType = argType.Elem()
	}
	if argType == nil || argType.Kind() != reflect.Struct {
		return params
	}

	for i := range argType.NumField() {
		field := argType.Field(i)
		if !field.IsExported() {
			continue
		}

		name := tagName(field.Tag.Get("json"))
		if name == "" {
			name = strings.ToLower(field.Name)
		}

		prop := params.Props[name]
		if prop == nil {
			continue
		}

		enrichToolPropFromField(prop, field.Type)
	}

	return params
}

func enrichToolPropFromField(prop *arktools.Prop, fieldType reflect.Type) {
	if prop == nil {
		return
	}

	baseType := baseFieldType(fieldType)
	if baseType == nil {
		return
	}

	if baseType.Kind() == reflect.Bool {
		if prop.Type == "boolean" {
			if len(prop.Enum) == 0 {
				prop.Enum = []any{true, false}
			}
			if prop.Default == nil {
				prop.Default = false
			}
		}
		return
	}

	if prop.Type != "string" {
		return
	}

	enumDesc, ok := enumDescriptorFromType(fieldType)
	if !ok || len(enumDesc.Options) == 0 {
		return
	}

	if len(prop.Enum) == 0 {
		prop.Enum = make([]any, 0, len(enumDesc.Options))
		for _, option := range enumDesc.Options {
			prop.Enum = append(prop.Enum, option.Value)
		}
	}
	if prop.Default == nil && enumDesc.DefaultValue != "" {
		prop.Default = enumDesc.DefaultValue
	}
}
