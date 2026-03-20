package config

type ChatMode string

const (
	ChatModeStandard ChatMode = "standard"
	ChatModeAgentic  ChatMode = "agentic"
)

func (m ChatMode) Normalize() ChatMode {
	switch m {
	case ChatModeAgentic:
		return ChatModeAgentic
	default:
		return ChatModeStandard
	}
}
