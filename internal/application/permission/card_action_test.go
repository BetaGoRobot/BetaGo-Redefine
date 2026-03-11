package permission

import (
	"testing"

	cardactionproto "github.com/BetaGoRobot/BetaGo-Redefine/pkg/cardaction"
)

func TestBuildPermissionGrantValueUsesStandardAction(t *testing.T) {
	payload := BuildPermissionGrantValue("permission.manage", "global", "ou_target", "", "")
	if payload[cardactionproto.ActionField] != cardactionproto.ActionPermissionGrant {
		t.Fatalf("unexpected action field: %q", payload[cardactionproto.ActionField])
	}
	if payload[cardactionproto.PermissionPointField] != "permission.manage" {
		t.Fatalf("unexpected permission point: %q", payload[cardactionproto.PermissionPointField])
	}
	if payload[cardactionproto.TargetUserIDField] != "ou_target" {
		t.Fatalf("unexpected target user id: %q", payload[cardactionproto.TargetUserIDField])
	}
}

func TestParseActionRequest(t *testing.T) {
	req, err := ParseActionRequest(&cardactionproto.Parsed{
		Name: cardactionproto.ActionPermissionRevoke,
		Value: map[string]any{
			cardactionproto.PermissionPointField: "config.write",
			cardactionproto.ScopeField:           "global",
			cardactionproto.TargetUserIDField:    "ou_target",
		},
	})
	if err != nil {
		t.Fatalf("ParseActionRequest() error = %v", err)
	}
	if req.Action != ActionRevoke || req.PermissionPoint != "config.write" || req.Scope != "global" || req.TargetOpenID != "ou_target" {
		t.Fatalf("unexpected request: %+v", req)
	}
}

func TestParseViewRequestPrefersFormValue(t *testing.T) {
	req, err := ParseViewRequest(&cardactionproto.Parsed{
		Name: cardactionproto.ActionPermissionView,
		Value: map[string]any{
			cardactionproto.TargetUserIDField: "ou_value",
		},
		FormValue: map[string]any{
			cardactionproto.TargetUserIDField: "ou_form",
		},
	})
	if err != nil {
		t.Fatalf("ParseViewRequest() error = %v", err)
	}
	if req.TargetOpenID != "ou_form" {
		t.Fatalf("unexpected target user id: %q", req.TargetOpenID)
	}
}

func TestBuildPermissionViewValueUsesStandardAction(t *testing.T) {
	payload := BuildPermissionViewValue("ou_target")
	if payload[cardactionproto.ActionField] != cardactionproto.ActionPermissionView {
		t.Fatalf("unexpected action field: %q", payload[cardactionproto.ActionField])
	}
	if payload[cardactionproto.TargetUserIDField] != "ou_target" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}
