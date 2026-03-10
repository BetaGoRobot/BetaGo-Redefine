package config

import "context"

func BuildConfigCardJSON(ctx context.Context, scope, chatID, userID string) (map[string]any, error) {
	return BuildConfigCardJSONWithOptions(ctx, scope, chatID, userID, ConfigCardViewOptions{})
}

func BuildConfigCardJSONWithOptions(ctx context.Context, scope, chatID, userID string, options ConfigCardViewOptions) (map[string]any, error) {
	card, err := BuildConfigCardWithOptions(ctx, scope, chatID, userID, options)
	if err != nil {
		return nil, err
	}
	return map[string]any(card), nil
}
