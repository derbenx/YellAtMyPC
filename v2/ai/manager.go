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
	IsLocal           bool
	Host              string
	Port              string
	GgufFile          string
	MmprojFile        string
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

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		m.cmd = nil
		return fmt.Errorf("llama-server exited immediately: %v", err)
	case <-time.After(2 * time.Second):
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
	if runtime.GOOS != "windows" {
		err := m.cmd.Process.Signal(syscallSignalZero())
		return err == nil
	}
	return true
}

// Action represents an automation action parsed from LLM reply XML
type Action struct {
	Tool      string   `json:"tool"`
	Text      string   `json:"text,omitempty"`
	Key       string   `json:"key,omitempty"`
	Modifiers []string `json:"modifiers,omitempty"`
	Button    string   `json:"button,omitempty"`
	Double    bool     `json:"double,omitempty"`
	X         int      `json:"x,omitempty"`
	Y         int      `json:"y,omitempty"`
	Name      string   `json:"name,omitempty"`
	Summary   string   `json:"summary,omitempty"` // Bullet-point or state summary
	Reply     string   `json:"reply,omitempty"`   // Spoken text to feed directly to TTS
}

// ParseActions extracts JSON tool action calls enclosed inside `<action>...</action>` tags
func ParseActions(reply string) []Action {
	var actions []Action

	startTag := "<action>"
	endTag := "</action>"

	temp := reply
	for {
		startIdx := strings.Index(temp, startTag)
		if startIdx == -1 {
			break
		}

		temp = temp[startIdx+len(startTag):]
		endIdx := strings.Index(temp, endTag)
		if endIdx == -1 {
			break
		}

		jsonStr := strings.TrimSpace(temp[:endIdx])
		temp = temp[endIdx+len(endTag):]

		var act Action
		if err := json.Unmarshal([]byte(jsonStr), &act); err == nil {
			actions = append(actions, act)
		} else {
			fmt.Printf("Debug: Action parse failed for string '%s': %v\n", jsonStr, err)
		}
	}

	return actions
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

	systemPrompt := config.PersonalityPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a helpful local PC voice assistant with access to computer tools. Respond concisely."
	}

	toolInstructions := `
You are a highly capable PC Automation Agent that can execute actions on the user's computer.
If the user asks you to perform a task (e.g. click somewhere, type text, or open an app), respond with a clear spoken text reply AND append one or more executable JSON XML <action> tags at the end of your response.

Supported Tools:
1. Type text:
   <action>{"tool": "type_text", "text": "hello"}</action>
2. Press keyboard shortcut/key (key names can be standard single keys, or media keys: volume_up, volume_down, mute):
   <action>{"tool": "press_key", "key": "s", "modifiers": ["control"]}</action>
   <action>{"tool": "press_key", "key": "volume_up"}</action>
3. Click mouse (button: "left" or "right", double: true/false):
   <action>{"tool": "click_mouse", "button": "left", "double": false}</action>
4. Move mouse smoothly to coordinates:
   <action>{"tool": "move_mouse", "x": 100, "y": 200}</action>
5. Launch allowed-listed app (notepad, calc, cmd, explorer, mspaint):
   <action>{"tool": "run_app", "name": "notepad"}</action>
6. Take screenshot to see where to click:
   <action>{"tool": "take_screenshot"}</action>
7. Read copied text selection:
   <action>{"tool": "read_selection"}</action>
8. Finalize spoken reply & summarize conversation status:
   <action>{"tool": "speak_reply", "summary": "A concise bullet-point summary of what actions were taken and the state of the conversation.", "reply": "The exact voice text that you want played out loud to the user via TTS."}</action>

Always finalize your multi-step actions by appending the 'speak_reply' tool call. This allows the system to clearly log the summary of what was accomplished while reading your reply out loud. Ensure the XML block has exact tag syntax.
`

	finalPrompt := fmt.Sprintf("%s\n\n%s", systemPrompt, toolInstructions)

	reqPayload := ChatCompletionRequest{
		Messages: []ChatMessage{
			{
				Role: "user",
				Content: []ChatContent{
					{
						Type: "text",
						Text: finalPrompt,
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
