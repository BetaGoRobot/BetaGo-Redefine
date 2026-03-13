package config

import (
	"sort"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/xcommand"
)

func (ConfigKey) CommandEnum() xcommand.EnumDescriptor {
	options := make([]xcommand.CommandArgOption, 0, len(configDefinitions))
	for _, def := range configDefinitions {
		options = append(options, xcommand.CommandArgOption{
			Value: string(def.Key),
			Label: string(def.Key) + " | " + def.Description,
		})
	}
	return xcommand.EnumDescriptor{Options: options}
}

type FeatureName string

func (FeatureName) CommandEnum() xcommand.EnumDescriptor {
	features := append([]Feature(nil), GetAllFeatures()...)
	sort.Slice(features, func(i, j int) bool {
		return features[i].Name < features[j].Name
	})

	options := make([]xcommand.CommandArgOption, 0, len(features))
	for _, feature := range features {
		options = append(options, xcommand.CommandArgOption{
			Value: feature.Name,
			Label: feature.Name + " | " + feature.Description,
		})
	}
	return xcommand.EnumDescriptor{Options: options}
}
