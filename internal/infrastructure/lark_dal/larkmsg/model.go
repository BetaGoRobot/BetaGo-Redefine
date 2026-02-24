package larkmsg

import (
	"fmt"

	"github.com/bytedance/sonic"
)

type PostMsg struct {
	Title   string         `json:"title"`
	Content PostMsgContent `json:"content"`
}

type PostMsgContent [][]ContentItemWrapper

type PostMsgContentItem interface {
	GetTag() string
}

// ContentItemWrapper 是用来承载不同类型 Item 的包装器
type ContentItemWrapper struct {
	Item PostMsgContentItem
}

// Marshalsonic 保证序列化时直接输出底层具体的结构体
func (w ContentItemWrapper) Marshalsonic() ([]byte, error) {
	return sonic.Marshal(w.Item)
}

// Unmarshalsonic 实现多态反序列化
func (w *ContentItemWrapper) UnmarshalJSON(data []byte) error {
	// 1. 先解析出 tag 字段
	var peek struct {
		Tag string `json:"tag"`
	}
	if err := sonic.Unmarshal(data, &peek); err != nil {
		return err
	}

	// 2. 根据 tag 将原始数据反序列化到对应的具体结构体中
	switch peek.Tag {
	case "img":
		var item PostMsgContentItemImg
		if err := sonic.Unmarshal(data, &item); err != nil {
			return err
		}
		w.Item = &item
	case "text":
		var item PostMsgContentItemText
		if err := sonic.Unmarshal(data, &item); err != nil {
			return err
		}
		w.Item = &item
	case "a":
		var item PostMsgContentItemA
		if err := sonic.Unmarshal(data, &item); err != nil {
			return err
		}
		w.Item = &item
	case "at":
		var item PostMsgContentItemAt
		if err := sonic.Unmarshal(data, &item); err != nil {
			return err
		}
		w.Item = &item
	case "media":
		var item PostMsgContentItemMedia
		if err := sonic.Unmarshal(data, &item); err != nil {
			return err
		}
		w.Item = &item
	case "emotion":
		var item PostMsgContentItemEmotion
		if err := sonic.Unmarshal(data, &item); err != nil {
			return err
		}
		w.Item = &item
	case "hr":
		var item PostMsgContentItemHR
		if err := sonic.Unmarshal(data, &item); err != nil {
			return err
		}
		w.Item = &item
	case "code_block":
		var item PostMsgContentItemCodeBlock
		if err := sonic.Unmarshal(data, &item); err != nil {
			return err
		}
		w.Item = &item
	default:
		return fmt.Errorf("larkmsg: unknown tag type '%s'", peek.Tag)
	}

	return nil
}

// ================= 具体类型定义 =================

type PostMsgContentItemImg struct {
	Tag      string `json:"tag"`
	ImageKey string `json:"image_key,omitempty"`
}

func (i *PostMsgContentItemImg) GetTag() string { return "img" }

type PostMsgContentItemText struct {
	Tag   string   `json:"tag"`
	Text  string   `json:"text,omitempty"`
	Style []string `json:"style,omitempty"`
}

func (i *PostMsgContentItemText) GetTag() string { return "text" }

type PostMsgContentItemA struct {
	Tag   string   `json:"tag"`
	Href  string   `json:"href,omitempty"`
	Text  string   `json:"text,omitempty"`
	Style []string `json:"style,omitempty"`
}

func (i *PostMsgContentItemA) GetTag() string { return "a" }

type PostMsgContentItemAt struct {
	Tag      string   `json:"tag"`
	UserID   string   `json:"user_id,omitempty"`
	UserName string   `json:"user_name,omitempty"`
	Style    []string `json:"style,omitempty"`
}

func (i *PostMsgContentItemAt) GetTag() string { return "at" }

type PostMsgContentItemMedia struct {
	Tag      string `json:"tag"`
	FileKey  string `json:"file_key,omitempty"`
	ImageKey string `json:"image_key,omitempty"`
}

func (i *PostMsgContentItemMedia) GetTag() string { return "media" }

type PostMsgContentItemEmotion struct {
	Tag       string `json:"tag"`
	EmojiType string `json:"emoji_type,omitempty"`
}

func (i *PostMsgContentItemEmotion) GetTag() string { return "emotion" }

type PostMsgContentItemHR struct {
	Tag string `json:"tag"`
}

func (i *PostMsgContentItemHR) GetTag() string { return "hr" }

type PostMsgContentItemCodeBlock struct {
	Tag      string `json:"tag"`
	Language string `json:"language,omitempty"`
	Text     string `json:"text,omitempty"`
}

func (i *PostMsgContentItemCodeBlock) GetTag() string { return "code_block" }
