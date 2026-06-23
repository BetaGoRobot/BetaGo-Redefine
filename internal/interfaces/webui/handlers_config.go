package webui

import (
	"net/http"
	"strconv"
	"strings"

	appconfig "github.com/BetaGoRobot/BetaGo-Redefine/internal/application/config"
	"github.com/bytedance/sonic"
)

// globalChatToken 是路由里代表全局作用域的特殊 chatID。
const globalChatToken = "global"

// resolveScope 把路由中的 chatID 转换成配置作用域与实际 chatID。
// "global" 映射 ScopeGlobal（chatID 置空），其它值映射 ScopeChat。
func resolveScope(chatID string) (appconfig.ConfigScope, string) {
	if chatID == globalChatToken {
		return appconfig.ScopeGlobal, ""
	}
	return appconfig.ScopeChat, chatID
}

func (s *Server) handleListFeatures(w http.ResponseWriter, r *http.Request) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	if chatID == "" {
		writeError(w, http.StatusBadRequest, "chat id is required")
		return
	}
	if s.cfg == nil {
		writeError(w, http.StatusServiceUnavailable, "config manager unavailable")
		return
	}
	_, scopedChatID := resolveScope(chatID)

	features := s.cfg.GetAllFeatures()
	views := make([]FeatureView, 0, len(features))
	for _, f := range features {
		views = append(views, FeatureView{
			Name:           f.Name,
			Description:    f.Description,
			Category:       f.Category,
			DefaultEnabled: f.DefaultEnabled,
			Enabled:        s.cfg.IsFeatureEnabled(r.Context(), f.Name, f.DefaultEnabled, scopedChatID, ""),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": views,
		"total": len(views),
	})
}

type setFeatureRequest struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) handleSetFeature(w http.ResponseWriter, r *http.Request) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	name := strings.TrimSpace(r.PathValue("name"))
	if chatID == "" || name == "" {
		writeError(w, http.StatusBadRequest, "chat id and feature name are required")
		return
	}
	if s.cfg == nil {
		writeError(w, http.StatusServiceUnavailable, "config manager unavailable")
		return
	}
	var req setFeatureRequest
	if err := sonic.ConfigDefault.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	scope, scopedChatID := resolveScope(chatID)

	var err error
	if req.Enabled {
		// 启用 = 取消屏蔽。
		err = s.cfg.UnblockFeature(r.Context(), name, scope, scopedChatID, "")
	} else {
		// 禁用 = 屏蔽。
		err = s.cfg.BlockFeature(r.Context(), name, scope, scopedChatID, "", "webui")
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":    name,
		"enabled": req.Enabled,
	})
}

func (s *Server) handleListConfigs(w http.ResponseWriter, r *http.Request) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	if chatID == "" {
		writeError(w, http.StatusBadRequest, "chat id is required")
		return
	}
	if s.cfg == nil {
		writeError(w, http.StatusServiceUnavailable, "config manager unavailable")
		return
	}
	_, scopedChatID := resolveScope(chatID)

	views := make([]ConfigView, 0)
	for _, key := range appconfig.GetAllConfigKeys() {
		def, ok := appconfig.GetConfigDefinition(key)
		if !ok {
			continue
		}
		value := s.resolveConfigValue(r, def, scopedChatID)
		view := ConfigView{
			Key:         string(def.Key),
			Description: def.Description,
			ValueType:   def.ValueType,
			Value:       value,
			IntMin:      def.IntMin,
			IntMax:      def.IntMax,
			ReadOnly:    def.ReadOnly,
			AllowCustom: def.AllowCustom,
		}
		for _, opt := range def.EnumOptions(value) {
			view.EnumOptions = append(view.EnumOptions, ConfigEnumOptionView{Text: opt.Text, Value: opt.Value})
		}
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": views,
		"total": len(views),
	})
}

// resolveConfigValue 按类型读取配置的生效值并归一化成字符串。
func (s *Server) resolveConfigValue(r *http.Request, def appconfig.ConfigDefinition, chatID string) string {
	switch def.ValueType {
	case "int":
		return strconv.Itoa(s.cfg.GetInt(r.Context(), def.Key, chatID, ""))
	case "bool":
		return strconv.FormatBool(s.cfg.GetBool(r.Context(), def.Key, chatID, ""))
	default:
		return s.cfg.GetString(r.Context(), def.Key, chatID, "")
	}
}

type setConfigRequest struct {
	Value string `json:"value"`
}

func (s *Server) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	keyStr := strings.TrimSpace(r.PathValue("key"))
	if chatID == "" || keyStr == "" {
		writeError(w, http.StatusBadRequest, "chat id and config key are required")
		return
	}
	if s.cfg == nil {
		writeError(w, http.StatusServiceUnavailable, "config manager unavailable")
		return
	}
	def, ok := appconfig.GetConfigDefinition(appconfig.ConfigKey(keyStr))
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown config key: "+keyStr)
		return
	}
	if def.ReadOnly {
		writeError(w, http.StatusForbidden, "config is read-only: "+keyStr)
		return
	}
	var req setConfigRequest
	if err := sonic.ConfigDefault.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	normalized, err := validateConfigValue(def, req.Value)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	scope, scopedChatID := resolveScope(chatID)
	if err := s.cfg.SetString(r.Context(), def.Key, scope, scopedChatID, "", normalized); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"key":   keyStr,
		"value": normalized,
	})
}

func (s *Server) handleDeleteConfig(w http.ResponseWriter, r *http.Request) {
	chatID := strings.TrimSpace(r.PathValue("chatID"))
	keyStr := strings.TrimSpace(r.PathValue("key"))
	if chatID == "" || keyStr == "" {
		writeError(w, http.StatusBadRequest, "chat id and config key are required")
		return
	}
	if s.cfg == nil {
		writeError(w, http.StatusServiceUnavailable, "config manager unavailable")
		return
	}
	def, ok := appconfig.GetConfigDefinition(appconfig.ConfigKey(keyStr))
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown config key: "+keyStr)
		return
	}
	scope, scopedChatID := resolveScope(chatID)
	if err := s.cfg.DeleteConfig(r.Context(), def.Key, scope, scopedChatID, ""); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": keyStr, "deleted": true})
}

// validateConfigValue 按配置类型与约束校验入参，返回归一化后的字符串值。
func validateConfigValue(def appconfig.ConfigDefinition, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	switch def.ValueType {
	case "int":
		n, err := strconv.Atoi(value)
		if err != nil {
			return "", errInvalidValue("expected integer value")
		}
		if def.IntMin != 0 || def.IntMax != 0 {
			if n < def.IntMin || n > def.IntMax {
				return "", errInvalidValue("value out of range")
			}
		}
		return strconv.Itoa(n), nil
	case "bool":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return "", errInvalidValue("expected boolean value")
		}
		return strconv.FormatBool(b), nil
	default:
		if def.HasEnumOptions() && !def.AllowCustom {
			for _, opt := range def.EnumOptions(value) {
				if opt.Value == value {
					return value, nil
				}
			}
			return "", errInvalidValue("value not in allowed enum options")
		}
		return value, nil
	}
}

type validationError struct{ msg string }

func (e validationError) Error() string { return e.msg }

func errInvalidValue(msg string) error { return validationError{msg: "invalid config value: " + msg} }
