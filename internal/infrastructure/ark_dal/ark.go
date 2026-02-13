package ark_dal

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
)

var (
	client         *arkruntime.Client
	arkConfig      *config.ArkConfig
	reasoningModel string
	normalModel    string
	embeddingModel string
	visionModel    string
)

func Init(config *config.ArkConfig) {
	arkConfig = config
	client = arkruntime.NewClientWithApiKey(config.APIKey)
	reasoningModel = config.ReasoningModel
	normalModel = config.NormalModel
	embeddingModel = config.EmbeddingModel
	visionModel = config.VisionModel
}
