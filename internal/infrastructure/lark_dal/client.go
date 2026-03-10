package lark_dal

import (
	"strings"
	"sync"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/config"
	lark "github.com/larksuite/oapi-sdk-go/v3"
)

var (
	clientRegistry   = make(map[string]*lark.Client)
	clientRegistryMu sync.RWMutex
)

func Client() *lark.Client { // for 外部调用
	conf := config.Get().LarkConfig
	if conf == nil {
		return nil
	}

	key := clientRegistryKey(conf.AppID, conf.AppSecret)
	clientRegistryMu.RLock()
	client := clientRegistry[key]
	clientRegistryMu.RUnlock()
	if client != nil {
		return client
	}

	clientRegistryMu.Lock()
	defer clientRegistryMu.Unlock()
	if client = clientRegistry[key]; client != nil {
		return client
	}
	client = lark.NewClient(conf.AppID, conf.AppSecret)
	clientRegistry[key] = client
	return client
}

func Init() {
	_ = Client()
}

func clientRegistryKey(appID, appSecret string) string {
	return strings.TrimSpace(appID) + ":" + strings.TrimSpace(appSecret)
}
