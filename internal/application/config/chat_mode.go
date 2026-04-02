package config

type ChatMode string

const (
	ChatModeStandard ChatMode = "standard"
)

func (m ChatMode) Normalize() ChatMode {
	return ChatModeStandard
}
