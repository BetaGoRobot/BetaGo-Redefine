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
	BaseInfo           *BaseInfo           `json:"base_info" yaml:"base_info" toml:"base_info"`
	DBConfig           *DBConfig           `json:"db_config" yaml:"db_config" toml:"db_config"`
	OtelConfig         *OtelConfig         `json:"otel_config" yaml:"otel_config" toml:"otel_config"`
	OpensearchConfig   *OpensearchConfig   `json:"opensearch_config" yaml:"opensearch_config" toml:"opensearch_config"`
	LarkConfig         *LarkConfig         `json:"lark_config" yaml:"lark_config" toml:"lark_config"`
	MinioConfig        *MinioConfig        `json:"minio_config" yaml:"minio_config" toml:"minio_config"`
	ArkConfig          *ArkConfig          `json:"ark_config" yaml:"ark_config" toml:"ark_config"`
	NeteaseMusicConfig *NeteaseMusicConfig `json:"netease_music_config" yaml:"netease_music_config" toml:"netease_music_config"`
	RateConfig         *RateConfig         `json:"rate_config" yaml:"rate_config" toml:"rate_config"`
	ProxyConfig        *ProxyConfig        `json:"proxy_config" yaml:"proxy_config" toml:"proxy_config"`
	AKToolConfig       *AKToolConfig       `json:"aktool_config" yaml:"aktool_config" toml:"aktool_config"`
	GotifyConfig       *GotifyConfig       `json:"gotify_config" yaml:"gotify_config" toml:"gotify_config"`
	RedisConfig        *RedisConfig        `json:"redis_config" yaml:"redis_config" toml:"redis_config"`
	KuttConfig         *KuttConfig         `json:"kutt_config" yaml:"kutt_config" toml:"kutt_config"`
}

type KuttConfig struct {
	BaseURL     string `json:"base_url" yaml:"base_url" toml:"base_url"`
	ExternalURL string `json:"external_url" yaml:"external_url" toml:"external_url"`
	APIKey      string `json:"api_key" yaml:"api_key" toml:"api_key"`
}

type RedisConfig struct {
	Addr     string `json:"addr" yaml:"addr" toml:"addr"`
	Password string `json:"password" yaml:"password" toml:"password"`
	DB       int    `json:"db" yaml:"db" toml:"db"`
}

type BaseInfo struct {
	RobotName string `json:"robot_name" yaml:"robot_name" toml:"robot_name"`
}

type GotifyConfig struct {
	URL              string `json:"url" yaml:"url" toml:"url"`
	ApplicationToken string `json:"application_token" yaml:"application_token" toml:"application_token"`
}

type AKToolConfig struct {
	BaseURL string `json:"base_url" yaml:"base_url" toml:"base_url"`
}
type NeteaseMusicConfig struct {
	BaseURL           string `json:"base_url" yaml:"base_url" toml:"base_url"`
	MusicCardInThread bool   `json:"music_card_in_thread" yaml:"music_card_in_thread" toml:"music_card_in_thread"`
	UserName          string `json:"user_name" yaml:"user_name" toml:"user_name"`
	PassWord          string `json:"pass_word" yaml:"pass_word" toml:"pass_word"`
	PlayerURL         string `json:"player_url" yaml:"player_url" toml:"player_url"`
}

type ProxyConfig struct {
	PrivateProxy string `json:"private_proxy" yaml:"private_proxy" toml:"private_proxy"`
}
type RateConfig struct {
	ReactionDefaultRate int `json:"reaction_default_rate" yaml:"reaction_default_rate" toml:"reaction_default_rate"`
	RepeatDefaultRate   int `json:"repeat_default_rate" yaml:"repeat_default_rate" toml:"repeat_default_rate"`
	ImitateDefaultRate  int `json:"imitate_default_rate" yaml:"imitate_default_rate" toml:"imitate_default_rate"`
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
	CollectorEndpoint  string `json:"collector_endpoint" yaml:"collector_endpoint" toml:"collector_endpoint"`
	TracerName         string `json:"tracer_name" yaml:"tracer_name" toml:"tracer_name"`
	ServiceName        string `json:"service_name" yaml:"service_name" toml:"service_name"`
	GrafanaURL         string `json:"grafana_url" yaml:"grafana_url" toml:"grafana_url"`
	JaegerDataSourceID string `json:"jaeger_data_source_id" yaml:"jaeger_data_source_id" toml:"jaeger_data_source_id"`
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
	AppID           string `json:"app_id" yaml:"app_id" toml:"app_id"`
	AppSecret       string `json:"app_secret" yaml:"app_secret" toml:"app_secret"`
	Encryption      string `json:"encryption" yaml:"encryption" toml:"encryption"`
	Verification    string `json:"verification" yaml:"verification" toml:"verification"`
	BotOpenID       string `json:"bot_open_id" yaml:"bot_open_id" toml:"bot_open_id"`
	WithDrawReplace bool   `json:"with_draw_replace" yaml:"with_draw_replace" toml:"with_draw_replace"`
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
	if config == nil {
		if os.Getenv("BETAGO_CONFIG_PATH") == "" {
			config = LoadFile(".dev/config.toml")
		} else {
			config = LoadFile(os.Getenv("BETAGO_CONFIG_PATH"))
		}
	}
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
