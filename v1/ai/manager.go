package ai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ServerConfig holds the configuration details for llama-server.
type ServerConfig struct {
	IsLocal        bool
	Host           string
	Port           string
	GgufFile       string
	MmprojFile     string
	PersonalityPrompt string
}

// LlamaManager coordinates local/remote llama-server and chat completion API calls.
type LlamaManager struct {
	cmd *exec.Cmd
}

// NewLlamaManager creates a new manager instance.
func NewLlamaManager() *LlamaManager {
	return &LlamaManager{}
}

// FindLocalFiles scans local directories to find possible servers, gguf, and mmproj files.
func FindLocalFiles() (servers []string, ggufs []string, mmprojs []string) {
	// Look for llama-server in "./llama"
	localLlamaDir := "llama"
	if info, err := os.Stat(localLlamaDir); err == nil && info.IsDir() {
		filepath.Walk(localLlamaDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() {
				name := strings.ToLower(info.Name())
				if strings.Contains(name, "llama-server") {
					servers = append(servers, path)
				}
			}
			return nil
		})
	}

	// Look for models in "./ai"
	aiDir := "ai"
	if info, err := os.Stat(aiDir); err == nil && info.IsDir() {
		filepath.Walk(aiDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() {
				name := strings.ToLower(info.Name())
				if strings.HasSuffix(name, ".gguf") {
					if strings.Contains(name, "mmproj") || strings.Contains(name, "mproj") {
						mmprojs = append(mmprojs, path)
					} else {
						ggufs = append(ggufs, path)
					}
				}
			}
			return nil
		})
	}

	return servers, ggufs, mmprojs
}

// StartLocalServer starts a local llama-server in a background process.
func (m *LlamaManager) StartLocalServer(binaryPath string, config ServerConfig) error {
	if m.cmd != nil {
		// Stop any existing process first
		_ = m.StopServer()
	}

	absBinary, err := filepath.Abs(binaryPath)
	if err != nil {
		absBinary = binaryPath
	}

	args := []string{
		"-m", config.GgufFile,
		"--mmproj", config.MmprojFile,
		"--host", config.Host,
		"--port", config.Port,
		"-n", "512",
	}

	cmd := exec.Command(absBinary, args...)

	// Create log file for server output to help with diagnostics
	logFile, err := os.Create("llama_server_output.log")
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("failed to start local llama-server: %w", err)
	}

	m.cmd = cmd

	// Wait briefly to see if it crashed immediately
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		m.cmd = nil
		return fmt.Errorf("llama-server exited immediately: %v", err)
	case <-time.After(2 * time.Second):
		// Seems to be running fine so far!
		return nil
	}
}

// StopServer kills the running local llama-server process if active.
func (m *LlamaManager) StopServer() error {
	if m.cmd != nil && m.cmd.Process != nil {
		var err error
		if runtime.GOOS == "windows" {
			err = m.cmd.Process.Kill()
		} else {
			// On Unix, try sending SIGTERM first, then Kill if needed
			_ = m.cmd.Process.Signal(os.Interrupt)
			time.Sleep(500 * time.Millisecond)
			err = m.cmd.Process.Kill()
		}
		m.cmd = nil
		return err
	}
	return nil
}

// IsRunning checks if the local llama-server process is currently running.
func (m *LlamaManager) IsRunning() bool {
	if m.cmd == nil || m.cmd.Process == nil {
		return false
	}
	// On Unix, sending signal 0 checks process existence. On Windows we can try Wait or rely on process handle
	if runtime.GOOS != "windows" {
		err := m.cmd.Process.Signal(syscallSignalZero())
		return err == nil
	}
	// Fallback for Windows or simple state tracking
	return true
}

// OpenAI structures for multimodal chat completion payload
type ChatMessage struct {
	Role    string        `json:"role"`
	Content []ChatContent `json:"content"`
}

type ChatContent struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	InputAudio *InputAudioData `json:"input_audio,omitempty"`
}

type InputAudioData struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

type ChatCompletionRequest struct {
	Messages []ChatMessage `json:"messages"`
}

type ChatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// SendAudioQuery encodes a raw WAV file to base64 and posts it to the /v1/chat/completions endpoint.
func (m *LlamaManager) SendAudioQuery(config ServerConfig, wavPath string) (string, error) {
	wavData, err := os.ReadFile(wavPath)
	if err != nil {
		return "", fmt.Errorf("failed to read WAV file: %w", err)
	}

	b64Wav := base64.StdEncoding.EncodeToString(wavData)

	personalityPrompt := config.PersonalityPrompt
	if personalityPrompt == "" {
		personalityPrompt = "You are a helpful local PC voice assistant. Respond concisely to the spoken audio."
	}

	reqPayload := ChatCompletionRequest{
		Messages: []ChatMessage{
			{
				Role: "user",
				Content: []ChatContent{
					{
						Type: "text",
						Text: personalityPrompt,
					},
					{
						Type: "input_audio",
						InputAudio: &InputAudioData{
							Data:   b64Wav,
							Format: "wav",
						},
					},
				},
			},
		},
	}

	jsonBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON payload: %w", err)
	}

	endpoint := fmt.Sprintf("http://%s:%s/v1/chat/completions", config.Host, config.Port)
	req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// 60-second timeout for local model audio inference
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request to llama-server failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llama-server returned error status %d: %s", resp.StatusCode, string(respBytes))
	}

	var completionResp ChatCompletionResponse
	if err := json.Unmarshal(respBytes, &completionResp); err != nil {
		return "", fmt.Errorf("failed to unmarshal JSON response: %w", err)
	}

	if completionResp.Error != nil {
		return "", fmt.Errorf("API error: %s", completionResp.Error.Message)
	}

	if len(completionResp.Choices) == 0 {
		return "", fmt.Errorf("no response choices returned by the server")
	}

	return completionResp.Choices[0].Message.Content, nil
}
