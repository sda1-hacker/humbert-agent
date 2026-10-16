package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// Load 只读取当前格式：默认值 < YAML < HUMBERT_RAG_* 环境变量。
// path 留空时使用 HUMBERT_RAG_CONFIG；加载不创建文件、不连接模型或数据库。
func Load(path string) (Config, error) {
	cfg := Default()
	v := viper.New()
	v.SetEnvPrefix("HUMBERT_RAG")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if err := register(v, reflect.ValueOf(cfg), "", true); err != nil {
		return Config{}, err
	}
	if trim(path) == "" {
		path = os.Getenv("HUMBERT_RAG_CONFIG")
	}
	if path = trim(path); path != "" {
		v.SetConfigFile(path)
		v.SetConfigType("yaml")
		if err := v.ReadInConfig(); err != nil {
			return Config{}, fmt.Errorf("rag config: read file: %w", err)
		}
	}

	if err := v.UnmarshalExact(&cfg, func(d *mapstructure.DecoderConfig) {
		d.DecodeHook = mapstructure.ComposeDecodeHookFunc(durationHook, mapstructure.StringToSliceHookFunc(","))
	}); err != nil {
		return Config{}, fmt.Errorf("rag config: decode: %w", err)
	}
	cfg.Database.Host, cfg.Database.Name, cfg.Database.User = trim(cfg.Database.Host), trim(cfg.Database.Name), trim(cfg.Database.User)
	cfg.Database.SSLMode = normalizeName(cfg.Database.SSLMode)
	cfg.CollectionID = trim(cfg.CollectionID)
	cfg.Embedding = cfg.Embedding.Effective()
	if cfg.Embedding.Provider == ProviderOllama {
		cfg.Embedding.APIKey = ""
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// register 从结构体推导可加载的字段，避免手写另一份配置键清单及默认值。
// 显式 BindEnv 让只存在于环境变量中的字段也参与 Viper.Unmarshal。
func register(v *viper.Viper, value reflect.Value, prefix string, defaults bool) error {
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			value = reflect.Zero(value.Type().Elem())
			defaults = false
		} else {
			value = value.Elem()
		}
	}
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		key := field.Tag.Get("mapstructure")
		if key == "-" {
			continue
		}
		if key == "" {
			key = strings.ToLower(field.Name)
		}
		if prefix != "" {
			key = prefix + "." + key
		}
		entry := value.Field(i)
		kind := entry.Kind()
		if kind == reflect.Struct || (kind == reflect.Pointer && entry.Type().Elem().Kind() == reflect.Struct) {
			if err := register(v, entry, key, defaults); err != nil {
				return err
			}
			continue
		}
		if err := v.BindEnv(key); err != nil {
			return err
		}
		if !defaults || (kind == reflect.Pointer && entry.IsNil()) || (kind == reflect.Map && entry.IsNil()) {
			continue
		}
		if entry.Type() == reflect.TypeFor[time.Duration]() {
			v.SetDefault(key, time.Duration(entry.Int()).String())
		} else if kind == reflect.Pointer && entry.Type().Elem() == reflect.TypeFor[time.Duration]() {
			v.SetDefault(key, time.Duration(entry.Elem().Int()).String())
		} else {
			v.SetDefault(key, entry.Interface())
		}
	}
	return nil
}

// durationHook 要求超时包含单位，避免把 YAML 中的 30 误当成 30 纳秒。
func durationHook(from, to reflect.Type, value any) (any, error) {
	if to != reflect.TypeFor[time.Duration]() {
		return value, nil
	}
	if from == to {
		return value, nil
	}
	if from.Kind() != reflect.String {
		return nil, fmt.Errorf("duration requires a unit, e.g. 30s or 2m")
	}
	return time.ParseDuration(value.(string))
}
