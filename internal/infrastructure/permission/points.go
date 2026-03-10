package permission

import "slices"

const (
	ScopeGlobal = "global"
	ScopeChat   = "chat"
	ScopeUser   = "user"

	PermissionPointManage      = "permission.manage"
	PermissionPointConfigWrite = "config.write"
)

type PointDefinition struct {
	Point           string
	Name            string
	Description     string
	Category        string
	SupportedScopes []string
}

var builtInPoints = []PointDefinition{
	{
		Point:           PermissionPointManage,
		Name:            "权限管理",
		Description:     "允许查看权限点，并为其他用户授予或撤销权限",
		Category:        "admin",
		SupportedScopes: []string{ScopeGlobal},
	},
	{
		Point:           PermissionPointConfigWrite,
		Name:            "全局配置写入",
		Description:     "允许修改当前机器人下的全局配置项和全局 feature 开关",
		Category:        "config",
		SupportedScopes: []string{ScopeGlobal},
	},
}

func ListPointDefinitions() []PointDefinition {
	result := make([]PointDefinition, len(builtInPoints))
	copy(result, builtInPoints)
	slices.SortFunc(result, func(a, b PointDefinition) int {
		switch {
		case a.Category < b.Category:
			return -1
		case a.Category > b.Category:
			return 1
		case a.Point < b.Point:
			return -1
		case a.Point > b.Point:
			return 1
		default:
			return 0
		}
	})
	return result
}

func LookupPointDefinition(point string) (PointDefinition, bool) {
	for _, item := range builtInPoints {
		if item.Point == point {
			return item, true
		}
	}
	return PointDefinition{}, false
}

func SupportsScope(def PointDefinition, scope string) bool {
	for _, item := range def.SupportedScopes {
		if item == scope {
			return true
		}
	}
	return false
}
