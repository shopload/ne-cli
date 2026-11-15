// Package config stores app credentials and tokens per profile.
//
// Files live in $NE_CLI_CONFIG_DIR, or <user config dir>/ne-cli, with 0600
// permissions. Environment variables override stored values.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const (
	DefaultAPIHost  = "https://api.next-engine.org"
	DefaultBaseHost = "https://base.next-engine.org"
)

// Profile is one Next Engine app registration plus its current tokens.
type Profile struct {
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	RedirectURI  string `json:"redirect_uri,omitempty"`
	APIHost      string `json:"api_host,omitempty"`
	BaseHost     string `json:"base_host,omitempty"`

	AccessToken         string `json:"access_token,omitempty"`
	AccessTokenEndDate  string `json:"access_token_end_date,omitempty"`
	RefreshToken        string `json:"refresh_token,omitempty"`
	RefreshTokenEndDate string `json:"refresh_token_end_date,omitempty"`
	// RotatedFrom is the access token that was exchanged for AccessToken. It
	// lets a stale NE_ACCESS_TOKEN in the environment be recognised.
	RotatedFrom string `json:"rotated_from,omitempty"`

	CompanyName string `json:"company_name,omitempty"`
	CompanyNEID string `json:"company_ne_id,omitempty"`
	UserName    string `json:"pic_name,omitempty"`
}

type File struct {
	Profiles map[string]*Profile `json:"profiles"`
}

// Store reads and writes the config file for a single profile.
type Store struct {
	Path    string
	Name    string
	file    File
	Profile *Profile
	// TokensFromEnv is true when the tokens in Profile came from NE_ACCESS_TOKEN.
	TokensFromEnv bool
}

// Dir returns the configuration directory.
func Dir() (string, error) {
	if d := os.Getenv("NE_CLI_CONFIG_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ne-cli"), nil
}

// Open loads the named profile ("" = NE_PROFILE or "default") and applies
// environment overrides.
func Open(name string) (*Store, error) {
	if name == "" {
		name = os.Getenv("NE_PROFILE")
	}
	if name == "" {
		name = "default"
	}
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	s := &Store{Path: filepath.Join(dir, "config.json"), Name: name}
	b, err := os.ReadFile(s.Path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &s.file); err != nil {
			return nil, errors.New("config file " + s.Path + " is not valid JSON: " + err.Error())
		}
	}
	if s.file.Profiles == nil {
		s.file.Profiles = map[string]*Profile{}
	}
	stored := s.file.Profiles[name]
	if stored == nil {
		stored = &Profile{}
		s.file.Profiles[name] = stored
	}
	p := *stored // effective view; env overrides never get written back
	s.Profile = &p
	override(&p.ClientID, "NE_CLIENT_ID")
	override(&p.ClientSecret, "NE_CLIENT_SECRET")
	override(&p.RedirectURI, "NE_REDIRECT_URI")
	override(&p.APIHost, "NE_API_HOST")
	override(&p.BaseHost, "NE_BASE_HOST")
	if p.APIHost == "" {
		p.APIHost = DefaultAPIHost
	}
	if p.BaseHost == "" {
		p.BaseHost = DefaultBaseHost
	}
	// Tokens rotate server-side. If the env token is the one we already
	// exchanged, keep using the newer stored pair.
	if at := os.Getenv("NE_ACCESS_TOKEN"); at != "" && at != stored.RotatedFrom && at != stored.AccessToken {
		p.AccessToken = at
		p.RefreshToken = os.Getenv("NE_REFRESH_TOKEN")
		p.AccessTokenEndDate, p.RefreshTokenEndDate = "", ""
		s.TokensFromEnv = true
	}
	return s, nil
}

func override(dst *string, env string) {
	if v := os.Getenv(env); v != "" {
		*dst = v
	}
}

// Update mutates the stored profile and writes the file atomically.
func (s *Store) Update(fn func(p *Profile)) error {
	stored := s.file.Profiles[s.Name]
	fn(stored)
	fn(s.Profile)
	return s.save()
}

// Profiles lists the stored profile names.
func (s *Store) Profiles() []string {
	var out []string
	for name := range s.file.Profiles {
		out = append(out, name)
	}
	return out
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.file, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}
