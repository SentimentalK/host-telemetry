package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

var ErrNotExist = fs.ErrNotExist

type Config struct {
	Endpoint string `yaml:"endpoint"`
	Token    string `yaml:"token"`
	HostID   string `yaml:"host_id"`
	HostName string `yaml:"host_name"`
	Listen   string `yaml:"listen"`
	WALDir   string `yaml:"wal_dir"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, ErrNotExist
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Config{}, err
	}
	return c, nil
}

func Save(path string, c Config) error {
	data, err := Marshal(c)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func Marshal(c Config) ([]byte, error) {
	return yaml.Marshal(c)
}

func (c Config) WithEnv(getenv func(string) string) Config {
	if v := getenv("HT_ENDPOINT"); v != "" {
		c.Endpoint = v
	}
	if v := getenv("HT_TOKEN"); v != "" {
		c.Token = v
	}
	if v := getenv("HT_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := getenv("HT_WAL_DIR"); v != "" {
		c.WALDir = v
	}
	return c
}
