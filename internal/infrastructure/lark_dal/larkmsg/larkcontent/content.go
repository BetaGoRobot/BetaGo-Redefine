package larkcontent

import (
	"iter"

	"github.com/BetaGoRobot/BetaGo-Redefine/pkg/utils"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type MsgConstraints interface {
	textMsg | imageMsg | fileMsg | stickerMsg | postMsg | *larkim.EventMessage
}

// text类型的消息
type textMsg struct {
	Text string `json:"text"`
}

// image类型的消息
type imageMsg struct {
	ImageKey string `json:"image_key"`
}

// file类型的消息
type fileMsg struct {
	FileKey string `json:"file_key"`
}

// 表情包类型的消息
type stickerMsg struct {
	FileKey string `json:"file_key"`
}

type contentData struct {
	Tag      string `json:"tag"`
	Text     string `json:"text"`
	ImageKey string `json:"image_key"`
	FileKey  string `json:"file_key"`
	UserID   string `json:"user_id"`
}
type Item struct {
	Tag     string `json:"tag"` // image text
	Content string `json:"content"`
}

// 对于收到的post类型消息，可以通过这样的方式来解析其中的内容
type postMsg struct {
	Title   string           `json:"title"`
	Content [][]*contentData `json:"content"`
}

// Trans2Item to be filled
//
//	@param msgType string
//	@param content string
//	@return itemList []*Item
//	@author kevinmatthe
//	@update 2025-04-30 14:04:48
func Trans2Item(msgType, content string) (itemList iter.Seq[*Item]) {
	return func(yield func(*Item) bool) {
		switch msgType {
		case "text": // text是处理过的，直接返回
			if !yield(&Item{Tag: "text", Content: content}) {
				return
			}
		case "post":
			res := &postMsg{}
			err := utils.UnmarshalStringPre(content, res)
			if err != nil {
				// post也被前置解析过了，直接返回
				if !yield(&Item{Tag: "text", Content: content}) {
					return
				}
				return
			}
			for _, ele := range res.Content {
				for _, ele2 := range ele {
					switch ele2.Tag {
					case "at":
						if !yield(&Item{Tag: "at", Content: ele2.UserID}) {
							return
						}
					case "text":
						if !yield(&Item{Tag: "text", Content: ele2.Text}) {
							return
						}
					case "image":
						if !yield(&Item{Tag: "image", Content: ele2.ImageKey}) {
							return
						}
					case "sticker":
						if !yield(&Item{Tag: "sticker", Content: ele2.FileKey}) {
							return
						}
					}
				}
			}
		case "image":
			res := utils.MustUnmarshalString[imageMsg](content)
			if !yield(&Item{Tag: "image", Content: res.ImageKey}) {
				return
			}
		case "file":
			res := utils.MustUnmarshalString[fileMsg](content)
			if !yield(&Item{Tag: "file", Content: res.FileKey}) {
				return
			}
		}
	}
}

// GetContentItemsSeq to be filled
//
//	@param msg T
//	@return msgType string
//	@return msgContent string
//	@author kevinmatthe
//	@update 2025-04-30 13:37:40
func GetContentItemsSeq[T MsgConstraints](msg T) iter.Seq[*Item] {
	switch m := any(msg).(type) {
	case *larkim.EventMessage:
		// 处理事件消息
		return Trans2Item(*m.MessageType, *m.Content)
	case *larkim.Message:
		// 处理普通消息
		return Trans2Item(*m.MsgType, *m.Body.Content)
	}
	return nil
}
