package ark_dal

import (
	"github.com/bytedance/gg/gptr"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime/model/responses"
)

func baseInputItem(sysPrompt, userPrompt string) []*responses.InputItem {
	return []*responses.InputItem{
		{
			Union: &responses.InputItem_InputMessage{
				InputMessage: &responses.ItemInputMessage{
					Role: responses.MessageRole_system,
					Content: []*responses.ContentItem{
						{
							Union: &responses.ContentItem_Text{
								Text: &responses.ContentItemText{
									Type: responses.ContentItemType_input_text,
									Text: sysPrompt,
								},
							},
						},
					},
				},
			},
		},
		{
			Union: &responses.InputItem_InputMessage{
				InputMessage: &responses.ItemInputMessage{
					Role: responses.MessageRole_user,
					Content: []*responses.ContentItem{
						{
							Union: &responses.ContentItem_Text{
								Text: &responses.ContentItemText{
									Type: responses.ContentItemType_input_text,
									Text: userPrompt,
								},
							},
						},
					},
				},
			},
		},
	}
}

func buildImageInputMessages(files ...string) []*responses.InputItem {
	items := make([]*responses.InputItem, 0, len(files))
	for _, file := range files {
		items = append(items, &responses.InputItem{
			Union: &responses.InputItem_InputMessage{
				InputMessage: &responses.ItemInputMessage{
					Role: responses.MessageRole_user,
					Content: []*responses.ContentItem{
						{
							Union: &responses.ContentItem_Image{
								Image: &responses.ContentItemImage{
									Type:     responses.ContentItemType_input_image,
									ImageUrl: gptr.Of(file),
								},
							},
						},
					},
				},
			},
		})
	}
	return items
}
