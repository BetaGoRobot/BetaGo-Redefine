package tools

type (
	FCMeta[T any] struct {
		ChatID string
		OpenID string
		Data   *T
	}
)
