package holiday

import (
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/ark_dal/tools"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// Init 初始化节假日服务
func Init() {
	// 初始化服务实例
	GetService()
}

// RegisterHolidayTools 注册节假日工具到工具实例
func RegisterHolidayTools(ins *tools.Impl[larkim.P2MessageReceiveV1]) {
	RegisterTools(ins)
}
