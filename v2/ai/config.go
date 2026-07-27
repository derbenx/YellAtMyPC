package ai

import (
	"encoding/json"
	"os"
)

// AppSettings represents the persistent settings file for YellAtMyPC
type AppSettings struct {
	IsLocal           bool              `json:"is_local"`
	Host              string            `json:"host"`
	Port              string            `json:"port"`
	GgufFile          string            `json:"gguf_file"`
	MmprojFile        string            `json:"mmproj_file"`
	LlamaFile         string            `json:"llama_file"`
	PersonalityPrompt string            `json:"personality_prompt"`
	MicrophoneName    string            `json:"microphone_name"`
	// V2 Safety Checklist & Whitelist persistence
	EnableMouse       bool              `json:"enable_mouse"`
	EnableKeys        bool              `json:"enable_keys"`
	EnableScreen      bool              `json:"enable_screen"`
	EnableApps        bool              `json:"enable_apps"`
	EnableCurl        bool              `json:"enable_curl"`
	AllowedAppsMap    map[string]string `json:"allowed_apps_map"`
	// Custom Hotkeys
	PTTKeyString      string            `json:"ptt_key_string"`
	PTTModifiers      []string          `json:"ptt_modifiers"`
	KillKeyString     string            `json:"kill_key_string"`
	KillModifiers     []string          `json:"kill_modifiers"`
}

// LoadSettings attempts to load settings from "settings.json"
func LoadSettings() (*AppSettings, error) {
	data, err := os.ReadFile("settings.json")
	if err != nil {
		return nil, err
	}
	var settings AppSettings
	err = json.Unmarshal(data, &settings)
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

// SaveSettings serializes settings to "settings.json"
func SaveSettings(settings *AppSettings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile("settings.json", data, 0644)
}
