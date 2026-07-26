package tts

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Speak plays the provided text out loud using the operating system's native TTS capabilities.
func Speak(text string) error {
	if text == "" {
		return nil
	}

	switch runtime.GOOS {
	case "windows":
		// Windows: Use PowerShell with Add-Type for SpeechSynthesizer or SAPI
		// SAPI via SpeechSynthesizer is extremely responsive and standard.
		psCmd := fmt.Sprintf(`Add-Type -AssemblyName System.Speech; $synth = New-Object System.Speech.Synthesis.SpeechSynthesizer; $synth.Speak("%s")`, escapeDoubleQuotes(text))
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("powershell SpeechSynthesizer failed: %w", err)
		}
		return nil

	case "linux":
		// Linux: Try espeak or spd-say
		if isCommandAvailable("spd-say") {
			cmd := exec.Command("spd-say", text)
			if err := cmd.Run(); err == nil {
				return nil
			}
		}
		if isCommandAvailable("espeak") {
			cmd := exec.Command("espeak", text)
			if err := cmd.Run(); err == nil {
				return nil
			}
		}
		return fmt.Errorf("neither spd-say nor espeak is installed on this Linux system")

	case "darwin":
		// macOS: Native 'say' command
		cmd := exec.Command("say", text)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("macos 'say' command failed: %w", err)
		}
		return nil

	default:
		return fmt.Errorf("unsupported operating system for TTS: %s", runtime.GOOS)
	}
}

// isCommandAvailable checks if a command exists in the system PATH.
func isCommandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// escapeDoubleQuotes escapes double quotes in a string to avoid breaking shell arguments.
func escapeDoubleQuotes(s string) string {
	res := ""
	for _, char := range s {
		if char == '"' {
			res += "`\"" // Powershell escapes double-quotes inside double-quotes with backtick
		} else if char == '$' || char == '`' {
			res += "`" + string(char) // Escape backticks and dollar signs to avoid shell injection/expansion
		} else {
			res += string(char)
		}
	}
	return res
}
