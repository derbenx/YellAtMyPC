package automation

import (
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/go-vgo/robotgo"
	"github.com/vcaesar/screenshot"
)

// AllowedApps lists the apps we permit executing via run_app
type AllowedApps struct {
	mutex   sync.RWMutex
	appMap  map[string]string // maps safe lower-case name to full executable path
}

// NewAllowedApps initializes allowed-listed apps with default safe examples
func NewAllowedApps() *AllowedApps {
	apps := &AllowedApps{
		appMap: make(map[string]string),
	}

	// Pre-populate standard safe apps based on operating system
	if runtime.GOOS == "windows" {
		apps.appMap["notepad"] = "notepad.exe"
		apps.appMap["calc"] = "calc.exe"
		apps.appMap["cmd"] = "cmd.exe"
		apps.appMap["explorer"] = "explorer.exe"
		apps.appMap["mspaint"] = "mspaint.exe"
	} else if runtime.GOOS == "linux" {
		apps.appMap["gedit"] = "gedit"
		apps.appMap["calc"] = "gnome-calculator"
		apps.appMap["terminal"] = "x-terminal-emulator"
	} else {
		apps.appMap["notes"] = "Notes"
		apps.appMap["calculator"] = "Calculator"
	}

	return apps
}

// IsAllowed checks if the given command is permitted
func (aa *AllowedApps) IsAllowed(name string) (string, bool) {
	aa.mutex.RLock()
	defer aa.mutex.RUnlock()

	path, ok := aa.appMap[strings.ToLower(strings.TrimSpace(name))]
	return path, ok
}

// SetAllowed adds or updates an allowed-list mapping
func (aa *AllowedApps) SetAllowed(name, path string) {
	aa.mutex.Lock()
	defer aa.mutex.Unlock()
	aa.appMap[strings.ToLower(strings.TrimSpace(name))] = path
}

// GetList returns the allowed list in key-value format for GUI config
func (aa *AllowedApps) GetList() map[string]string {
	aa.mutex.RLock()
	defer aa.mutex.RUnlock()

	copyMap := make(map[string]string)
	for k, v := range aa.appMap {
		copyMap[k] = v
	}
	return copyMap
}

// RemoveAllowed removes an app from the allowed list
func (aa *AllowedApps) RemoveAllowed(name string) {
	aa.mutex.Lock()
	defer aa.mutex.Unlock()
	delete(aa.appMap, strings.ToLower(strings.TrimSpace(name)))
}

// AutomationEngine coordinates calling robotgo functions securely
type AutomationEngine struct {
	AllowedApps  *AllowedApps
	EnableMouse  bool
	EnableKeys   bool
	EnableScreen bool
	EnableApps   bool
}

// NewAutomationEngine creates a configured engine
func NewAutomationEngine() *AutomationEngine {
	return &AutomationEngine{
		AllowedApps:  NewAllowedApps(),
		EnableMouse:  true,
		EnableKeys:   true,
		EnableScreen: true,
		EnableApps:   true,
	}
}

// TypeText types strings into active elements
func (e *AutomationEngine) TypeText(text string) error {
	if !e.EnableKeys {
		return fmt.Errorf("keyboard actions are disabled in safety checklist")
	}
	robotgo.TypeStr(text)
	return nil
}

// PressKey triggers keystrokes and special key/shortcut combos (e.g. volume_up, alt+tab)
func (e *AutomationEngine) PressKey(key string, modifiers []string) error {
	if !e.EnableKeys {
		return fmt.Errorf("keyboard actions are disabled in safety checklist")
	}

	key = strings.ToLower(strings.TrimSpace(key))

	// Handle special media keys
	switch key {
	case "volume_up":
		robotgo.KeyTap("audio_vol_up")
		return nil
	case "volume_down":
		robotgo.KeyTap("audio_vol_down")
		return nil
	case "mute":
		robotgo.KeyTap("audio_mute")
		return nil
	}

	// Prepare standardized modifiers slice in the exact []string format expected by robotgo
	stdModifiers := make([]string, len(modifiers))
	for i, m := range modifiers {
		mLower := strings.ToLower(strings.TrimSpace(m))
		// Standardize modifier name to "ctrl" to support Windows/Linux perfectly
		if mLower == "control" || mLower == "ctrl" {
			mLower = "ctrl"
		}
		stdModifiers[i] = mLower
	}

	if len(stdModifiers) > 0 {
		robotgo.KeyTap(key, stdModifiers) // Pass standard []string directly
	} else {
		robotgo.KeyTap(key)
	}

	return nil
}

// ClickMouse performs mouse clicks at current location
func (e *AutomationEngine) ClickMouse(button string, doubleClick bool) error {
	if !e.EnableMouse {
		return fmt.Errorf("mouse actions are disabled in safety checklist")
	}

	btn := "left"
	if strings.ToLower(strings.TrimSpace(button)) == "right" {
		btn = "right"
	}

	robotgo.Click(btn, doubleClick)
	return nil
}

// MoveMouse moves the cursor instantly to absolute coordinates (lightning fast!)
func (e *AutomationEngine) MoveMouse(x, y int) error {
	if !e.EnableMouse {
		return fmt.Errorf("mouse actions are disabled in safety checklist")
	}
	robotgo.Move(x, y)
	return nil
}

// DragMouse holds down the mouse button and drags smoothly to destination coordinates
func (e *AutomationEngine) DragMouse(x, y int) error {
	if !e.EnableMouse {
		return fmt.Errorf("mouse actions are disabled in safety checklist")
	}
	robotgo.DragSmooth(x, y)
	return nil
}

// GetMousePos returns the active cursor coordinates
func (e *AutomationEngine) GetMousePos() (int, int) {
	return robotgo.Location()
}

// TakeScreenshot captures the active primary screen and saves it as a PNG
func (e *AutomationEngine) TakeScreenshot(filePath string) error {
	if !e.EnableScreen {
		return fmt.Errorf("screenshots are disabled in safety checklist")
	}

	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.Capture(bounds.Min.X, bounds.Min.Y, bounds.Dx(), bounds.Dy())
	if err != nil {
		return fmt.Errorf("failed to capture screen: %w", err)
	}

	f, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create screenshot file: %w", err)
	}
	defer f.Close()

	err = png.Encode(f, img)
	if err != nil {
		return fmt.Errorf("failed to encode screenshot as PNG: %w", err)
	}

	return nil
}

// RunApp launches allowed-listed application commands safely
func (e *AutomationEngine) RunApp(name string) error {
	if !e.EnableApps {
		return fmt.Errorf("launching apps is disabled in safety checklist")
	}

	path, ok := e.AllowedApps.IsAllowed(name)
	if !ok {
		return fmt.Errorf("app '%s' is not in the allowed list", name)
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd.exe", "/c", "start", "", path)
	} else {
		cmd = exec.Command(path)
	}

	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("failed to start allowed app '%s': %w", name, err)
	}

	return nil
}

// ReadSelection triggers a Ctrl+C/Cmd+C and reads clipboard text
func (e *AutomationEngine) ReadSelection() (string, error) {
	if !e.EnableKeys {
		return "", fmt.Errorf("keyboard actions are disabled in safety checklist")
	}

	// Backup clipboard
	oldClip, _ := robotgo.ReadAll()

	// Tap copy shortcut using explicit []string format
	if runtime.GOOS == "darwin" {
		robotgo.KeyTap("c", []string{"command"})
	} else {
		robotgo.KeyTap("c", []string{"ctrl"}) // Guaranteed Ctrl+C on Windows
	}

	// Let OS register clipboard update
	robotgo.MilliSleep(150)

	newClip, err := robotgo.ReadAll()
	if err != nil {
		return "", fmt.Errorf("failed to read clipboard: %w", err)
	}

	// Restore original clipboard
	if oldClip != "" {
		_ = robotgo.WriteAll(oldClip)
	}

	return newClip, nil
}

// GetScreenSize returns the screen width and height
func (e *AutomationEngine) GetScreenSize() (int, int) {
	return robotgo.GetScreenSize()
}
