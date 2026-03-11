package config

import "context"

func BuildConfigCardJSON(ctx context.Context, scope, chatID, openID string) (map[string]any, error) {
	return BuildConfigCardJSONWithOptions(ctx, scope, chatID, openID, ConfigCardViewOptions{})
}

func BuildConfigCardJSONWithOptions(ctx context.Context, scope, chatID, openID string, options ConfigCardViewOptions) (map[string]any, error) {
	card, err := BuildConfigCardWithOptions(ctx, scope, chatID, openID, options)
	if err != nil {
		return nil, err
	}
	return map[string]any(card), nil
}
