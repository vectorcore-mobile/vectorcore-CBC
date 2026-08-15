package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server        Server        `yaml:"server"`
	Database      Database      `yaml:"database"`
	CBS           CBS           `yaml:"cbs"`
	SBcAP         SBcAP         `yaml:"sbcap"`
	Logging       Logging       `yaml:"logging"`
	CBE           CBE           `yaml:"cbe"`
	CellInventory CellInventory `yaml:"cell_inventory"`
}

type Server struct {
	ListenAddress   string        `yaml:"listen_address"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

type Logging struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}
type Database struct {
	Path           string        `yaml:"path"`
	BusyTimeout    time.Duration `yaml:"busy_timeout"`
	ExpiryInterval time.Duration `yaml:"expiry_interval"`
}
type CBS struct {
	DefaultMessageIdentifier uint16            `yaml:"default_message_identifier"`
	MessageIdentifiers       map[string]uint16 `yaml:"message_identifiers"`
	AllowPLMNWide            bool              `yaml:"allow_plmn_wide"`
}
type SBcAP struct {
	Enabled          bool          `yaml:"enabled"`
	PLMN             string        `yaml:"plmn"`
	Peers            []SBcAPPeer   `yaml:"peers"`
	RepetitionPeriod uint16        `yaml:"repetition_period"`
	Broadcasts       uint16        `yaml:"broadcasts"`
	ResponseTimeout  time.Duration `yaml:"response_timeout"`
	// ReconnectMin/ReconnectMax bound the backoff Publisher.Run uses when
	// (re)dialing an MME peer association - the CBC maintains this
	// connection persistently and independently of alert traffic (TS 29.168
	// requires the CBC to establish it), so a dropped association must be
	// retried indefinitely rather than only on the next Publish call.
	ReconnectMin time.Duration `yaml:"reconnect_min"`
	ReconnectMax time.Duration `yaml:"reconnect_max"`
	// LocalAddress pins the outbound SCTP association to a single local IP
	// instead of letting the OS multi-home across every local interface.
	// Some MMEs authorize CBC peers by a single source IP; a multi-homed
	// association reports as e.g. "127.0.0.1/10.0.0.5/172.17.0.1" on the
	// MME side, which never matches such an allowlist. Empty leaves the OS
	// default (multi-homed) behavior unchanged.
	LocalAddress string `yaml:"local_address"`
}
type SBcAPPeer struct {
	Name    string `yaml:"name"`
	Address string `yaml:"address"`
}

// CellInventory configures the LTE cell-inventory subsystem. It shares the
// CBC's existing SQLite database (Database.Path) rather than a database of
// its own; there is intentionally no PostgreSQL configuration yet, though
// the repository interfaces behind this feature are written to allow one
// later without an API change.
type CellInventory struct {
	Enabled            bool   `yaml:"enabled"`
	MaxImportSizeBytes int64  `yaml:"max_import_size_bytes"`
	DefaultImportMode  string `yaml:"default_import_mode"`
}

type CBE struct {
	Address            string        `yaml:"address"`
	Domain             string        `yaml:"domain"`
	Username           string        `yaml:"username"`
	Password           string        `yaml:"password"`
	Resource           string        `yaml:"resource"`
	TLSMode            string        `yaml:"tls_mode"`
	InsecureSkipVerify bool          `yaml:"insecure_skip_verify"`
	ReconnectMin       time.Duration `yaml:"reconnect_min"`
	ReconnectMax       time.Duration `yaml:"reconnect_max"`
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return Config{}, err
	}
	c.applyDefaults()
	return c, c.Validate()
}

func (c *Config) applyDefaults() {
	if c.Server.ShutdownTimeout <= 0 {
		c.Server.ShutdownTimeout = 10 * time.Second
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.File == "" {
		c.Logging.File = "cbc.log"
	}
	if c.Database.BusyTimeout <= 0 {
		c.Database.BusyTimeout = 5 * time.Second
	}
	if c.Database.ExpiryInterval <= 0 {
		c.Database.ExpiryInterval = time.Minute
	}
	if c.CBS.MessageIdentifiers == nil {
		c.CBS.MessageIdentifiers = map[string]uint16{}
	}
	if c.SBcAP.RepetitionPeriod == 0 {
		c.SBcAP.RepetitionPeriod = 30
	}
	if c.SBcAP.Broadcasts == 0 {
		c.SBcAP.Broadcasts = 4
	}
	if c.SBcAP.ResponseTimeout <= 0 {
		c.SBcAP.ResponseTimeout = 10 * time.Second
	}
	if c.SBcAP.ReconnectMin <= 0 {
		c.SBcAP.ReconnectMin = time.Second
	}
	if c.SBcAP.ReconnectMax <= 0 {
		c.SBcAP.ReconnectMax = 30 * time.Second
	}
	if c.CBE.Resource == "" {
		c.CBE.Resource = "vectorcore-cbc"
	}
	if c.CBE.TLSMode == "" {
		c.CBE.TLSMode = "plain"
	}
	if c.CBE.ReconnectMin <= 0 {
		c.CBE.ReconnectMin = time.Second
	}
	if c.CBE.ReconnectMax <= 0 {
		c.CBE.ReconnectMax = 30 * time.Second
	}
	if c.CellInventory.Enabled {
		if c.CellInventory.MaxImportSizeBytes <= 0 {
			c.CellInventory.MaxImportSizeBytes = 10 * 1024 * 1024
		}
		if c.CellInventory.DefaultImportMode == "" {
			c.CellInventory.DefaultImportMode = "validate-only"
		}
	}
}

func (c Config) Validate() error {
	if c.Server.ListenAddress == "" {
		return fmt.Errorf("server.listen_address is required")
	}
	if c.Database.Path == "" {
		return fmt.Errorf("database.path is required")
	}
	if c.Database.BusyTimeout <= 0 || c.Database.ExpiryInterval <= 0 {
		return fmt.Errorf("database timeouts must be positive")
	}
	if c.CBS.DefaultMessageIdentifier == 0 || c.CBS.DefaultMessageIdentifier == 0xffff {
		return fmt.Errorf("cbs.default_message_identifier must be a usable 16-bit value")
	}
	for severity, id := range c.CBS.MessageIdentifiers {
		if strings.TrimSpace(severity) == "" || id == 0 || id == 0xffff {
			return fmt.Errorf("cbs.message_identifiers contains an invalid mapping")
		}
	}
	if c.SBcAP.Enabled {
		if len(strings.ReplaceAll(c.SBcAP.PLMN, "-", "")) < 5 {
			return fmt.Errorf("sbcap.plmn is required when SBcAP is enabled")
		}
		if len(c.SBcAP.Peers) == 0 {
			return fmt.Errorf("sbcap.peers is required when SBcAP is enabled")
		}
		for _, p := range c.SBcAP.Peers {
			if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Address) == "" {
				return fmt.Errorf("each SBcAP peer requires name and address")
			}
		}
		if c.SBcAP.ReconnectMin <= 0 || c.SBcAP.ReconnectMax < c.SBcAP.ReconnectMin {
			return fmt.Errorf("invalid sbcap reconnect backoff")
		}
	}
	switch strings.ToLower(c.Logging.Level) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("logging.level must be debug, info, warn, or error")
	}
	e := c.CBE
	if e.Address == "" || e.Domain == "" || e.Username == "" || e.Password == "" {
		return fmt.Errorf("cbe address, domain, username and password are required")
	}
	if e.Resource == "" {
		return fmt.Errorf("cbe.resource is required")
	}
	if e.TLSMode != "plain" && e.TLSMode != "direct_tls" {
		return fmt.Errorf("cbe.tls_mode must be plain or direct_tls")
	}
	if e.ReconnectMin <= 0 || e.ReconnectMax < e.ReconnectMin {
		return fmt.Errorf("invalid cbe reconnect backoff")
	}
	if c.CellInventory.Enabled {
		switch c.CellInventory.DefaultImportMode {
		case "validate-only", "merge", "replace":
		default:
			return fmt.Errorf("cell_inventory.default_import_mode must be validate-only, merge, or replace")
		}
		if c.CellInventory.MaxImportSizeBytes <= 0 {
			return fmt.Errorf("cell_inventory.max_import_size_bytes must be positive")
		}
	}
	return nil
}
