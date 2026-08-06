package config

import (
	"os"
	"testing"
	"time"
)

type testCfg struct {
	Name        string        `env:"TEST_NAME" required:"true"`
	Port        int           `env:"TEST_PORT" default:"8080"`
	Debug       bool          `env:"TEST_DEBUG" default:"false"`
	Timeout     time.Duration `env:"TEST_TIMEOUT" default:"30s"`
	AllowedOrgs []string      `env:"TEST_ORGS" default:"a,b,c"`
}

func TestLoadHappy(t *testing.T) {
	t.Setenv("TEST_NAME", "model-relay")
	t.Setenv("TEST_DEBUG", "true")
	t.Setenv("TEST_TIMEOUT", "5s")
	var c testCfg
	if err := Load(&c); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Name != "model-relay" {
		t.Errorf("Name = %q", c.Name)
	}
	if c.Port != 8080 {
		t.Errorf("Port = %d", c.Port)
	}
	if !c.Debug {
		t.Errorf("Debug should be true")
	}
	if c.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v", c.Timeout)
	}
	if len(c.AllowedOrgs) != 3 || c.AllowedOrgs[0] != "a" {
		t.Errorf("AllowedOrgs = %v", c.AllowedOrgs)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	os.Unsetenv("TEST_NAME")
	var c testCfg
	if err := Load(&c); err == nil {
		t.Fatal("expected error for missing required")
	}
}
