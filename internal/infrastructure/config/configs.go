package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

var config *BaseConfig

type BaseConfig struct {
	// NOTE: 强依赖
	// 关系数据库
	DBConfig         *DBConfig         `json:"db_config" yaml:"db_config" toml:"db_config"`
	OtelConfig       *OtelConfig       `json:"otel_config" yaml:"otel_config" toml:"otel_config"`
	OpensearchConfig *OpensearchConfig `json:"opensearch_config" yaml:"opensearch_config" toml:"opensearch_config"`
	LarkConfig       *LarkConfig       `json:"lark_config" yaml:"lark_config" toml:"lark_config"`
	MinioConfig      *MinioConfig      `json:"minio_config" yaml:"minio_config" toml:"minio_config"`
	ArkConfig        *ArkConfig        `json:"ark_config" yaml:"ark_config" toml:"ark_config"`
}

type DBConfig struct {
	Host            string `json:"host" yaml:"host" toml:"host"`
	Port            int    `json:"port" yaml:"port" toml:"port"`
	User            string `json:"user" yaml:"user" toml:"user"`
	Password        string `json:"password" yaml:"password" toml:"password"`
	DBName          string `json:"dbname" yaml:"dbname" toml:"dbname"`
	SSLMode         string `json:"sslmode" yaml:"sslmode" toml:"sslmode"`
	Timezone        string `json:"timezone" yaml:"timezone" toml:"timezone"`
	ApplicationName string `json:"application_name" yaml:"application_name" toml:"application_name"`
	SearchPath      string `json:"search_path" yaml:"search_path" toml:"search_path"`
}

type OtelConfig struct {
	CollectorEndpoint string `json:"collector_endpoint" yaml:"collector_endpoint" toml:"collector_endpoint"`
	TracerName        string `json:"tracer_name" yaml:"tracer_name" toml:"tracer_name"`
	ServiceName       string `json:"service_name" yaml:"service_name" toml:"service_name"`
	GrafanaURL        string `json:"grafana_url" yaml:"grafana_url" toml:"grafana_url"`
}

type OpensearchConfig struct {
	Domain   string `json:"domain" yaml:"domain" toml:"domain"`
	User     string `json:"user" yaml:"user" toml:"user"`
	Password string `json:"password" yaml:"password" toml:"password"`

	LarkCardActionIndex string `json:"lark_card_action_index" yaml:"lark_card_action_index" toml:"lark_card_action_index"`
	LarkChunkIndex      string `json:"lark_chunk_index" yaml:"lark_chunk_index" toml:"lark_chunk_index"`
	LarkMsgIndex        string `json:"lark_msg_index" yaml:"lark_msg_index" toml:"lark_msg_index"`
}

type MinioConfig struct {
	Internal   *MinioConfigInner `json:"internal" yaml:"internal" toml:"internal"`
	External   *MinioConfigInner `json:"external" yaml:"external" toml:"external"`
	AK         string            `json:"ak_id" yaml:"ak" toml:"ak"`
	SK         string            `json:"sk" yaml:"sk" toml:"sk"`
	ExpireTime string            `json:"expire_time" yaml:"expire_time" toml:"expire_time"`
}

type MinioConfigInner struct {
	Endpoint string `json:"endpoint" yaml:"endpoint" toml:"endpoint"`
	UseSSL   bool   `json:"use_ssl" yaml:"use_ssl" toml:"use_ssl"`
}
type ArkConfig struct {
	APIKey string `json:"api_key" yaml:"api_key" toml:"api_key"`

	VisionModel    string `json:"vision_model" yaml:"vision_model" toml:"vision_model"`
	ReasoningModel string `json:"reasoning_model" yaml:"reasoning_model" toml:"reasoning_model"`
	NormalModel    string `json:"normal_model" yaml:"normal_model" toml:"normal_model"`
	EmbeddingModel string `json:"embedding_model" yaml:"embedding_model" toml:"embedding_model"`
	ChunkModel     string `json:"chunk_model" yaml:"chunk_model" toml:"chunk_model"`
}

type LarkConfig struct {
	AppID        string `json:"app_id" yaml:"app_id" toml:"app_id"`
	AppSecret    string `json:"app_secret" yaml:"app_secret" toml:"app_secret"`
	Encryption   string `json:"encryption" yaml:"encryption" toml:"encryption"`
	Verification string `json:"verification" yaml:"verification" toml:"verification"`
	BotOpenID    string `json:"bot_open_id" yaml:"bot_open_id" toml:"bot_open_id"`
}

func NewConfigs() *BaseConfig {
	return &BaseConfig{}
}

func LoadFile(path string) *BaseConfig {
	config = NewConfigs()
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	err = toml.Unmarshal(data, config)
	if err != nil {
		panic(err)
	}
	return config
}

func Get() *BaseConfig {
	return config
}

func (c *DBConfig) DSN() string {
	sb := strings.Builder{}
	if c.User != "" {
		sb.WriteString(fmt.Sprintf("user=%s ", c.User))
	}
	if c.Password != "" {
		sb.WriteString(fmt.Sprintf("password=%s ", c.Password))
	}
	if c.DBName != "" {
		sb.WriteString(fmt.Sprintf("dbname=%s ", c.DBName))
	}
	if c.Host != "" {
		sb.WriteString(fmt.Sprintf("host=%s ", c.Host))
	}
	if c.Port != 0 {
		sb.WriteString(fmt.Sprintf("port=%d ", c.Port))
	}
	if c.SSLMode != "" {
		sb.WriteString(fmt.Sprintf("sslmode=%s ", c.SSLMode))
	}
	if c.Timezone != "" {
		sb.WriteString(fmt.Sprintf("TimeZone=%s ", c.Timezone))
	}
	if c.ApplicationName != "" {
		sb.WriteString(fmt.Sprintf("application_name=%s ", c.ApplicationName))
	}
	if c.SearchPath != "" {
		sb.WriteString(fmt.Sprintf("search_path=%s ", c.SearchPath))
	}
	return sb.String()
}
