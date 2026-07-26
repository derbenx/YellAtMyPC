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
		psCmd := fmt.Sprintf(`Add-Type -AssemblyName System.Speech; $synth = New-Object System.Speech.Synthesis.SpeechSynthesizer; $synth.Speak("%s")`, escapeDoubleQuotes(text))
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("powershell SpeechSynthesizer failed: %w", err)
		}
		return nil

	case "linux":
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
		cmd := exec.Command("say", text)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("macos 'say' command failed: %w", err)
		}
		return nil

	default:
		return fmt.Errorf("unsupported operating system for TTS: %s", runtime.GOOS)
	}
}

func isCommandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func escapeDoubleQuotes(s string) string {
	res := ""
	for _, char := range s {
		if char == '"' {
			res += "`\""
		} else if char == '$' || char == '`' {
			res += "`" + string(char)
		} else {
			res += string(char)
		}
	}
	return res
}
