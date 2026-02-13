package tools

import "github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"

type Prop struct {
	Type  string  `json:"type"`
	Desc  string  `json:"description"`
	Items []*Prop `json:"items"`
}

type Param struct {
	Type     string           `json:"type"`
	Props    map[string]*Prop `json:"properties"`
	Required []string         `json:"required"`
}

func NewParams(typ string) *Param {
	return &Param{
		Type:     typ,
		Props:    make(map[string]*Prop),
		Required: make([]string, 0),
	}
}

func (p *Param) AddProp(name string, prop *Prop) *Param {
	p.Props[name] = prop
	return p
}

func (p *Param) AddRequired(name string) *Param {
	p.Required = append(p.Required, name)
	return p
}

func (p *Param) JSON() []byte {
	return utils.MustMarshal(p)
}

// func registerHistorySearch() {
// 	params := NewParams("object").
// 		AddProp("keywords", &Prop{
// 			Type: "array",
// 			Desc: "需要检索的关键词列表",
// 			Items: []*Prop{
// 				{
// 					Type: "string",
// 					Desc: "关键词",
// 				},
// 			},
// 		}).
// 		AddProp("user_id", &Prop{
// 			Type: "string",
// 			Desc: "用户ID",
// 		}).
// 		AddProp("start_time", &Prop{
// 			Type: "string",
// 			Desc: "开始时间，格式为YYYY-MM-DD HH:MM:SS",
// 		}).
// 		AddProp("end_time", &Prop{
// 			Type: "string",
// 			Desc: "结束时间，格式为YYYY-MM-DD HH:MM:SS",
// 		}).
// 		AddProp("top_k", &Prop{
// 			Type: "number",
// 			Desc: "返回的结果数量",
// 		}).
// 		AddRequired("keywords")
// 	fcu := NewFunctionCallUnit().
// 		Name(ToolSearchHistory).Desc("根据输入的关键词搜索相关的历史对话记录").
// 		Params(params).Func(HybridSearch)
// 	M().Add(fcu)
// }
