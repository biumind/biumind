// Package config provides 12-factor environment-based config loading.
// Every BiuMind service uses this pattern for consistency.
package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Load populates a struct from environment variables based on `env:"NAME"` tags.
// Supported types: string, int, int64, bool, time.Duration, []string (comma-separated).
// Use `default:"value"` tag for defaults; `required:"true"` to fail-fast.
func Load(target any) error {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("config.Load: target must be *struct")
	}
	v = v.Elem()
	t := v.Type()

	var missing []string
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		envName := field.Tag.Get("env")
		if envName == "" {
			continue
		}
		raw, found := os.LookupEnv(envName)
		if !found {
			if def := field.Tag.Get("default"); def != "" {
				raw = def
			} else if field.Tag.Get("required") == "true" {
				missing = append(missing, envName)
				continue
			} else {
				continue
			}
		}
		if err := assign(v.Field(i), raw); err != nil {
			return fmt.Errorf("config: field %s (%s): %w", field.Name, envName, err)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("config: missing required env vars: %s", strings.Join(missing, ", "))
	}
	return nil
}

func assign(field reflect.Value, raw string) error {
	switch field.Kind() {
	case reflect.String:
		field.SetString(raw)
	case reflect.Int, reflect.Int32, reflect.Int64:
		// 检查是否是 time.Duration
		if field.Type() == reflect.TypeOf(time.Duration(0)) {
			d, err := time.ParseDuration(raw)
			if err != nil {
				return err
			}
			field.SetInt(int64(d))
			return nil
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return err
		}
		field.SetInt(n)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		field.SetBool(b)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return err
		}
		field.SetFloat(f)
	case reflect.Slice:
		if field.Type().Elem().Kind() == reflect.String {
			parts := strings.Split(raw, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			field.Set(reflect.ValueOf(parts))
		} else {
			return fmt.Errorf("unsupported slice type: %s", field.Type())
		}
	default:
		return fmt.Errorf("unsupported field kind: %s", field.Kind())
	}
	return nil
}
