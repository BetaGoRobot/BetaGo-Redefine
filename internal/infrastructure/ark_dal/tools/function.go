package tools

type (
	FCMeta[T any] struct {
		ChatID string
		UserID string
		Data   *T
	}
)
