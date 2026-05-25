package tools

import (
	"context"
	"testing"

	"github.com/bytedance/gg/gresult"
)

func TestToolsEmitStrictFunctionTools(t *testing.T) {
	ins := New[string]()
	ins.Add(NewUnit[string]().
		Name("lookup").
		Desc("lookup data").
		Params(NewParams("object").AddProp("query", &Prop{Type: "string", Desc: "query"})).
		Func(func(context.Context, string, FCMeta[string]) gresult.R[string] {
			return gresult.OK("ok")
		}))

	got := ins.Tools()
	if len(got) != 1 {
		t.Fatalf("tool count = %d, want 1", len(got))
	}
	fn := got[0].GetToolFunction()
	if fn == nil {
		t.Fatal("expected function tool")
	}
	if fn.Strict == nil || !fn.GetStrict() {
		t.Fatalf("strict = %#v, want true", fn.Strict)
	}
}
