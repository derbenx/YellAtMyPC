package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"YellAtMyPC/v1/ai"
	"YellAtMyPC/v1/audio"
	"YellAtMyPC/v1/tts"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/driver/mobile"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"golang.design/x/hotkey"
)

type readOnlyEntry struct {
	widget.Entry
}

func newReadOnlyEntry() *readOnlyEntry {
	e := &readOnlyEntry{}
	e.MultiLine = true
	e.Wrapping = fyne.TextWrapWord
	e.ExtendBaseWidget(e)
	return e
}

func (e *readOnlyEntry) TypedRune(r rune)            {}
func (e *readOnlyEntry) TypedKey(k *fyne.KeyEvent)    {}
func (e *readOnlyEntry) FocusGained()                 {}
func (e *readOnlyEntry) FocusLost()                   {}

type AppState struct {
	serverConfig    ai.ServerConfig
	llamaMgr        *ai.LlamaManager
	recorder        *audio.Recorder
	isRecording     bool
	recordingMutex  sync.Mutex
	statusLabel     *widget.Label
	transcribeArea  *readOnlyEntry
	replyArea       *readOnlyEntry
	summaryArea     *readOnlyEntry
	serverStatus    *widget.Label
	win             fyne.Window

	// Auto-save debounce timer
	saveTimer *time.Timer

	// Context Memory
	lastSummary string

	// Active selected microphone
	selectedDeviceID unsafe.Pointer
	micSelect        *widget.Select
	micDeviceList    []audio.CaptureDevice

	// Personality Inputs (synchronized)
	personalityEntryMain *widget.Entry

	// Setup controls
	localRadio   *widget.RadioGroup
	hostEntry    *widget.Entry
	portEntry    *widget.Entry
	ggufSelect   *widget.Select
	mmprojSelect *widget.Select
	llamaSelect  *widget.Select
	launchBtn    *widget.Button
	saveBtn      *widget.Button

	// Hotkeys
	pttHotkey    *hotkey.Hotkey
	killHotkey   *hotkey.Hotkey
	hotkeyMutex  sync.Mutex

	// Hotkey GUI fields
	pttKeyEntry  *widget.Entry
	pttModsEntry *widget.Entry
	killKeyEntry *widget.Entry
	killModsEntry *widget.Entry

	// Safety Toggles
	enableCurlCheck     *widget.Check
	enableDatetimeCheck *widget.Check
	enableCurl          bool
	enableDatetime      bool
}

func main() {
	myApp := app.NewWithID("com.yellatmypc.app")
	myWindow := myApp.NewWindow("YellAtMyPC - Push To Talk AI Assistant")
	myWindow.Resize(fyne.NewSize(700, 520))

	recorder, err := audio.NewRecorder()
	if err != nil {
		log.Printf("Warning: Failed to initialize audio recorder: %v", err)
	}

	state := &AppState{
		serverConfig: ai.ServerConfig{
			IsLocal:           true,
			Host:              "127.0.0.1",
			Port:              "8080",
			PersonalityPrompt: "You are a helpful local PC voice assistant. Respond concisely to the spoken audio.",
		},
		llamaMgr: ai.NewLlamaManager(),
		recorder: recorder,
		win:      myWindow,
	}

	// Setup Personality multi-line input box directly on the Main Page
	state.personalityEntryMain = widget.NewMultiLineEntry()
	state.personalityEntryMain.SetText(state.serverConfig.PersonalityPrompt)
	state.personalityEntryMain.SetMinRowsVisible(6)
	state.personalityEntryMain.Wrapping = fyne.TextWrapWord // Word wrap enabled!
	state.personalityEntryMain.OnChanged = func(text string) {
		state.serverConfig.PersonalityPrompt = text

		// Auto-save debounced 1 minute after last change
		state.recordingMutex.Lock()
		if state.saveTimer != nil {
			state.saveTimer.Stop()
		}
		state.saveTimer = time.AfterFunc(1*time.Minute, func() {
			state.saveConfigurationSilent()
		})
		state.recordingMutex.Unlock()
	}

	// Build Tab 1: Main Push To Talk Tab (with standard, non-deprecated widget.NewLabel)
	state.statusLabel = widget.NewLabel("Idle. Press and Hold Button or Pause/Break to Talk.")
	state.statusLabel.Alignment = fyne.TextAlignCenter
	state.statusLabel.TextStyle = fyne.TextStyle{Bold: true}

	state.transcribeArea = newReadOnlyEntry()
	state.transcribeArea.SetPlaceHolder("Current status details and process logs...")
	state.transcribeArea.SetMinRowsVisible(4) // 4 lines visible height

	state.replyArea = newReadOnlyEntry()
	state.replyArea.SetPlaceHolder("AI spoken reply will appear here...")

	state.summaryArea = newReadOnlyEntry()
	state.summaryArea.SetPlaceHolder("Conversation memory context and turn summaries...")

	// Create our custom green press-and-hold button widget
	holdButton := newHoldButton("Push & Hold to Talk", func() {
		state.startRecordingFlow()
	}, func() {
		state.stopRecordingAndProcessFlow()
	})

	// Clear History Button
	clearHistoryBtn := widget.NewButtonWithIcon("Clear Conversation History", theme.DeleteIcon(), func() {
		state.lastSummary = ""
		state.summaryArea.SetText("")
		state.replyArea.SetText("")
		state.transcribeArea.SetText("Conversation history cleared.")
		_ = tts.Speak("Conversation history cleared.")
	})

	// Layout the Voice Chat page in a clean 50/50 Left/Right Dashboard split
	// Left column:
	//   - Title
	//   - Personality Entry
	//   - Status label (above PTT)
	//   - PTT Button
	//   - Status Log Area (4 lines tall, high-contrast, word wrapped)
	leftSide := container.NewVBox(
		widget.NewLabelWithStyle("AI Voice Controls", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("AI Personality Prompt:"),
		state.personalityEntryMain,
		state.statusLabel, // Idle Status right above the green PTT button
		holdButton,
		widget.NewLabel("System Status Log:"),
		container.NewGridWrap(fyne.NewSize(320, 110), state.transcribeArea), // ~4 lines tall
	)

	// Right column:
	//   - Conversation Summary (previously where status log was)
	//   - Clear History Button (placed under summary area)
	//   - Spoken AI Reply (below)
	rightSide := container.NewVBox(
		widget.NewLabel("AI Memory & Conversation Summary:"),
		container.NewGridWrap(fyne.NewSize(320, 140), state.summaryArea),
		clearHistoryBtn, // Placed beautifully directly under the summary box!
		widget.NewLabel("AI Spoken Reply Text:"),
		container.NewGridWrap(fyne.NewSize(320, 140), state.replyArea),
	)

	mainTabContent := container.NewBorder(
		nil, nil, nil, nil,
		container.NewGridWithColumns(2, leftSide, rightSide),
	)

	// Build Tab 2: Setup/Config Tab
	state.serverStatus = widget.NewLabel("Local Server Status: Stopped")

	state.localRadio = widget.NewRadioGroup([]string{"Local Server (Self-Hosted)", "Network / Remote Server"}, func(selected string) {
		if selected == "Local Server (Self-Hosted)" {
			state.serverConfig.IsLocal = true
			if state.hostEntry != nil {
				state.hostEntry.SetText("127.0.0.1")
				state.hostEntry.Disable()
			}
			if state.ggufSelect != nil {
				state.ggufSelect.Enable()
			}
			if state.mmprojSelect != nil {
				state.mmprojSelect.Enable()
			}
			if state.llamaSelect != nil {
				state.llamaSelect.Enable()
			}
			if state.launchBtn != nil {
				state.launchBtn.Enable()
			}
		} else {
			state.serverConfig.IsLocal = false
			if state.hostEntry != nil {
				state.hostEntry.Enable()
			}
			if state.ggufSelect != nil {
				state.ggufSelect.Disable()
			}
			if state.mmprojSelect != nil {
				state.mmprojSelect.Disable()
			}
			if state.llamaSelect != nil {
				state.llamaSelect.Disable()
			}
			if state.launchBtn != nil {
				state.launchBtn.Disable()
			}
		}
	})

	state.hostEntry = widget.NewEntry()
	state.hostEntry.SetText("127.0.0.1")
	state.hostEntry.Disable()

	state.portEntry = widget.NewEntry()
	state.portEntry.SetText("8080")

	// Microphone selection dropdown on the setup tab
	state.micSelect = widget.NewSelect(nil, func(selected string) {
		for _, dev := range state.micDeviceList {
			if dev.Name == selected {
				state.selectedDeviceID = dev.ID
				log.Printf("Selected capture microphone: %s", selected)
				break
			}
		}
	})

	state.ggufSelect = widget.NewSelect(nil, nil)
	state.mmprojSelect = widget.NewSelect(nil, nil)
	state.llamaSelect = widget.NewSelect(nil, nil)

	state.launchBtn = widget.NewButtonWithIcon("Launch Local Llama", theme.MediaPlayIcon(), func() {
		state.launchLocalServer()
	})

	state.localRadio.SetSelected("Local Server (Self-Hosted)")

	state.saveBtn = widget.NewButtonWithIcon("Save Configuration", theme.DocumentSaveIcon(), func() {
		state.saveConfiguration()
	})

	refreshBtn := widget.NewButtonWithIcon("Scan relative directories", theme.ViewRefreshIcon(), func() {
		state.scanFiles()
		state.refreshMicrophones()
	})

	state.pttKeyEntry = widget.NewEntry()
	state.pttKeyEntry.SetPlaceHolder("PTT Key Code (e.g. 0x13)")
	state.pttKeyEntry.SetText("0x13")

	state.pttModsEntry = widget.NewEntry()
	state.pttModsEntry.SetPlaceHolder("PTT Modifiers (e.g. none)")

	state.killKeyEntry = widget.NewEntry()
	state.killKeyEntry.SetPlaceHolder("Kill Key Code (e.g. 0x5a)")
	state.killKeyEntry.SetText("0x5a")

	state.killModsEntry = widget.NewEntry()
	state.killModsEntry.SetPlaceHolder("Kill Modifiers (e.g. win,alt)")
	state.killModsEntry.SetText("win,alt")

	hotkeyCard := widget.NewCard("Hotkey Settings", "Configure custom global keyboard hotkeys",
		container.NewVBox(
			container.NewGridWithColumns(2,
				container.NewVBox(widget.NewLabel("PTT Key Code (Hex/Dec):"), state.pttKeyEntry),
				container.NewVBox(widget.NewLabel("PTT Modifiers (comma list):"), state.pttModsEntry),
			),
			container.NewGridWithColumns(2,
				container.NewVBox(widget.NewLabel("Kill Switch Key Code (Hex/Dec):"), state.killKeyEntry),
				container.NewVBox(widget.NewLabel("Kill Switch Modifiers (comma list):"), state.killModsEntry),
			),
		),
	)

	configTabContent := container.NewVScroll(container.NewVBox(
		widget.NewCard("Connection Type", "", state.localRadio),
		container.NewGridWithColumns(2,
			container.NewVBox(widget.NewLabel("Host IP:"), state.hostEntry),
			container.NewVBox(widget.NewLabel("Port:"), state.portEntry),
		),
		widget.NewLabel("Select Microphone:"),
		state.micSelect,
		widget.NewCard("Local Llama discovery (relative paths)", "",
			container.NewVBox(
				state.serverStatus,
				container.NewGridWithColumns(2,
					container.NewVBox(widget.NewLabel("Choose GGUF Model:"), state.ggufSelect),
					container.NewVBox(widget.NewLabel("Choose Multimodal mproj:"), state.mmprojSelect),
				),
				widget.NewLabel("Choose Llama Server Executable:"),
				state.llamaSelect,
				container.NewGridWithColumns(3,
					refreshBtn,
					state.launchBtn,
					widget.NewButtonWithIcon("Stop Llama", theme.MediaStopIcon(), func() {
						state.stopLocalServer()
					}),
				),
			),
		),
		hotkeyCard,
		container.NewHBox(layout.NewSpacer(), state.saveBtn),
	))

	// Build Tab 2: Safety Checklist Tab
	state.enableCurlCheck = widget.NewCheck("Enable HTTP Curl Requests (Web searches, JSON API queries)", func(checked bool) {
		state.enableCurl = checked
	})
	state.enableCurlCheck.SetChecked(true)
	state.enableCurl = true

	state.enableDatetimeCheck = widget.NewCheck("Enable Datetime Retrieval (Check current date/time)", func(checked bool) {
		state.enableDatetime = checked
	})
	state.enableDatetimeCheck.SetChecked(true)
	state.enableDatetime = true

	safetyTabContent := container.NewVBox(
		widget.NewCard("Safety Gate & Checklist", "Restrict what the AI assistant can execute autonomously",
			container.NewVBox(
				widget.NewLabelWithStyle("Enable or disable specific tools globally:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
				state.enableCurlCheck,
				state.enableDatetimeCheck,
			),
		),
	)

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("Voice Chat", theme.HomeIcon(), mainTabContent),
		container.NewTabItemWithIcon("Safety Checklist", theme.ConfirmIcon(), safetyTabContent),
		container.NewTabItemWithIcon("Setup / Config", theme.SettingsIcon(), configTabContent),
	)

	myWindow.SetContent(tabs)

	// Scan folders and microphones synchronously on startup on the main thread (No fyne.Do thread warning!)
	state.scanFilesSync()
	state.refreshMicrophonesSync()

	// Load persistent settings if available
	state.loadPersistentSettings()

	// Setup Global Hotkey on a background thread
	go state.setupGlobalHotkey()

	myWindow.SetOnClosed(func() {
		if state.recorder != nil {
			state.recorder.Close()
		}
		_ = state.llamaMgr.StopServer()
	})

	myWindow.ShowAndRun()
}

func (state *AppState) loadPersistentSettings() {
	set, err := ai.LoadSettings()
	if err != nil {
		log.Printf("No existing settings file found or load failed: %v", err)
		return
	}

	state.serverConfig.IsLocal = set.IsLocal
	state.serverConfig.Host = set.Host
	state.serverConfig.Port = set.Port
	state.serverConfig.PersonalityPrompt = set.PersonalityPrompt

	fyne.Do(func() {
		state.hostEntry.SetText(set.Host)
		state.portEntry.SetText(set.Port)
		state.personalityEntryMain.SetText(set.PersonalityPrompt)

		if set.IsLocal {
			state.localRadio.SetSelected("Local Server (Self-Hosted)")
			state.ggufSelect.SetSelected(set.GgufFile)
			state.mmprojSelect.SetSelected(set.MmprojFile)
			state.llamaSelect.SetSelected(set.LlamaFile)
		} else {
			state.localRadio.SetSelected("Network / Remote Server")
		}

		if set.MicrophoneName != "" {
			state.micSelect.SetSelected(set.MicrophoneName)
		}

		// Safety Checklist toggles
		state.enableCurl = set.EnableCurl
		state.enableCurlCheck.SetChecked(set.EnableCurl)
		state.enableDatetime = set.EnableScreen
		state.enableDatetime = true
		if set.PTTKeyString != "" {
			state.enableDatetime = set.EnableKeys
		}
		state.enableDatetimeCheck.SetChecked(state.enableDatetime)

		// Hotkey GUI fields
		if set.PTTKeyString != "" {
			state.pttKeyEntry.SetText(set.PTTKeyString)
		}
		state.pttModsEntry.SetText(strings.Join(set.PTTModifiers, ","))
		if set.KillKeyString != "" {
			state.killKeyEntry.SetText(set.KillKeyString)
		}
		state.killModsEntry.SetText(strings.Join(set.KillModifiers, ","))
	})

	log.Println("Successfully loaded persistent settings from settings.json")
}

func (state *AppState) saveConfigurationSilent() {
	state.serverConfig.Host = state.hostEntry.Text
	state.serverConfig.Port = state.portEntry.Text
	state.serverConfig.PersonalityPrompt = state.personalityEntryMain.Text

	if state.serverConfig.IsLocal {
		state.serverConfig.GgufFile = state.ggufSelect.Selected
		state.serverConfig.MmprojFile = state.mmprojSelect.Selected
	}

	pttMods := strings.Split(state.pttModsEntry.Text, ",")
	var cleanedPttMods []string
	for _, m := range pttMods {
		m = strings.TrimSpace(m)
		if m != "" && m != "none" {
			cleanedPttMods = append(cleanedPttMods, m)
		}
	}

	killMods := strings.Split(state.killModsEntry.Text, ",")
	var cleanedKillMods []string
	for _, m := range killMods {
		m = strings.TrimSpace(m)
		if m != "" && m != "none" {
			cleanedKillMods = append(cleanedKillMods, m)
		}
	}

	// Keep existing V2 fields from being overwritten if they exist in file
	var existingMap map[string]string
	var existingEnableMouse, existingEnableKeys, existingEnableScreen, existingEnableApps bool
	existingEnableMouse = true
	existingEnableKeys = true
	existingEnableScreen = true
	existingEnableApps = true

	set, err := ai.LoadSettings()
	if err == nil {
		existingMap = set.AllowedAppsMap
		existingEnableMouse = set.EnableMouse
		existingEnableKeys = set.EnableKeys
		existingEnableScreen = set.EnableScreen
		existingEnableApps = set.EnableApps
	}

	settings := &ai.AppSettings{
		IsLocal:           state.serverConfig.IsLocal,
		Host:              state.hostEntry.Text,
		Port:              state.portEntry.Text,
		GgufFile:          state.ggufSelect.Selected,
		MmprojFile:        state.mmprojSelect.Selected,
		LlamaFile:         state.llamaSelect.Selected,
		PersonalityPrompt: state.personalityEntryMain.Text,
		MicrophoneName:    state.micSelect.Selected,
		EnableMouse:       existingEnableMouse,
		EnableKeys:        existingEnableKeys,
		EnableScreen:      existingEnableScreen,
		EnableApps:        existingEnableApps,
		EnableCurl:        state.enableCurl,
		AllowedAppsMap:    existingMap,
		PTTKeyString:      strings.TrimSpace(state.pttKeyEntry.Text),
		PTTModifiers:      cleanedPttMods,
		KillKeyString:     strings.TrimSpace(state.killKeyEntry.Text),
		KillModifiers:     cleanedKillMods,
	}

	_ = ai.SaveSettings(settings)

	// Re-register hotkeys dynamically on save!
	go state.setupGlobalHotkey()
}

func (state *AppState) saveConfiguration() {
	state.saveConfigurationSilent()
	dialog.ShowInformation("Configuration Saved", "Setup configurations saved successfully to settings.json.", state.win)
}

func (state *AppState) refreshMicrophonesSync() {
	if state.recorder == nil {
		return
	}
	devices, err := state.recorder.GetCaptureDevices()
	if err != nil {
		log.Printf("Error scanning microphones: %v", err)
		return
	}
	state.micDeviceList = devices

	var options []string
	for _, d := range devices {
		options = append(options, d.Name)
	}

	state.micSelect.Options = options
	if len(options) > 0 {
		state.micSelect.SetSelected(options[0])
	}
}

func (state *AppState) refreshMicrophones() {
	fyne.Do(func() {
		state.refreshMicrophonesSync()
	})
}

// Custom Green HoldButton subclassing widget.Button directly
type holdButton struct {
	widget.Button
	onPress   func()
	onRelease func()
}

func newHoldButton(text string, onPress, onRelease func()) *holdButton {
	b := &holdButton{
		onPress:   onPress,
		onRelease: onRelease,
	}
	b.Text = text
	b.Importance = widget.SuccessImportance // Makes the button Green automatically!
	b.ExtendBaseWidget(b)
	return b
}

func (b *holdButton) Dragged(*fyne.DragEvent) {}
func (b *holdButton) DragEnd()                {}

func (b *holdButton) TouchDown(*mobile.TouchEvent) {
	if b.onPress != nil {
		b.onPress()
	}
}

func (b *holdButton) TouchUp(*mobile.TouchEvent) {
	if b.onRelease != nil {
		b.onRelease()
	}
}

func (b *holdButton) TouchCancel(*mobile.TouchEvent) {
	if b.onRelease != nil {
		b.onRelease()
	}
}

func (b *holdButton) MouseDown(ev *desktop.MouseEvent) {
	if b.onPress != nil {
		b.onPress()
	}
}

func (b *holdButton) MouseUp(ev *desktop.MouseEvent) {
	if b.onRelease != nil {
		b.onRelease()
	}
}

func (state *AppState) scanFilesSync() {
	servers, ggufs, mmprojs := ai.FindLocalFiles()

	state.ggufSelect.Options = ggufs
	state.mmprojSelect.Options = mmprojs
	state.llamaSelect.Options = servers

	if len(ggufs) > 0 {
		state.ggufSelect.SetSelected(ggufs[0])
	} else {
		state.ggufSelect.ClearSelected()
	}

	if len(mmprojs) > 0 {
		state.mmprojSelect.SetSelected(mmprojs[0])
	} else {
		state.mmprojSelect.ClearSelected()
	}

	if len(servers) > 0 {
		state.llamaSelect.SetSelected(servers[0])
	} else {
		state.llamaSelect.ClearSelected()
	}
}

func (state *AppState) scanFiles() {
	fyne.Do(func() {
		state.scanFilesSync()
	})
}

func (state *AppState) launchLocalServer() {
	state.saveConfiguration()

	if state.llamaSelect.Selected == "" {
		dialog.ShowError(fmt.Errorf("please select a llama-server executable first"), state.win)
		return
	}
	if state.serverConfig.GgufFile == "" {
		dialog.ShowError(fmt.Errorf("please select a GGUF model file first"), state.win)
		return
	}
	if state.serverConfig.MmprojFile == "" {
		dialog.ShowError(fmt.Errorf("please select a multimodal mproj file first"), state.win)
		return
	}

	state.serverStatus.SetText("Local Server Status: Launching...")
	err := state.llamaMgr.StartLocalServer(state.llamaSelect.Selected, state.serverConfig)
	if err != nil {
		state.serverStatus.SetText("Local Server Status: Failed to Start")
		dialog.ShowError(fmt.Errorf("error starting llama-server: %v", err), state.win)
		return
	}

	state.serverStatus.SetText("Local Server Status: Running")
	dialog.ShowInformation("Llama Server Launched", fmt.Sprintf("Server launched successfully on port %s", state.serverConfig.Port), state.win)
}

func (state *AppState) stopLocalServer() {
	err := state.llamaMgr.StopServer()
	if err != nil {
		dialog.ShowError(fmt.Errorf("error stopping server: %v", err), state.win)
		return
	}
	state.serverStatus.SetText("Local Server Status: Stopped")
	dialog.ShowInformation("Llama Server Stopped", "Local server process was shut down.", state.win)
}

func (state *AppState) startRecordingFlow() {
	state.recordingMutex.Lock()
	defer state.recordingMutex.Unlock()

	if state.isRecording {
		return
	}

	state.isRecording = true
	fyne.Do(func() {
		state.statusLabel.SetText("🎤 Recording... Release when finished speaking.")
		state.transcribeArea.SetText("Capturing audio frames...")
	})

	if state.recorder != nil {
		err := state.recorder.Start(state.selectedDeviceID)
		if err != nil {
			state.isRecording = false
			fyne.Do(func() {
				state.statusLabel.SetText("Error starting microphone.")
				state.transcribeArea.SetText(fmt.Sprintf("Microphone error: %v", err))
			})
		}
	}
}

func (state *AppState) stopRecordingAndProcessFlow() {
	state.recordingMutex.Lock()
	defer state.recordingMutex.Unlock()

	if !state.isRecording {
		return
	}

	state.isRecording = false
	fyne.Do(func() {
		state.statusLabel.SetText("⌛ Stopped. Processing audio & querying AI...")
	})

	if state.recorder == nil {
		fyne.Do(func() {
			state.statusLabel.SetText("No active microphone available.")
		})
		return
	}

	// SYNCHRONOUSLY capture and stop the recorder to avoid any concurrent race conditions
	pcmBytes, err := state.recorder.Stop()
	if err != nil {
		fyne.Do(func() {
			state.statusLabel.SetText("Error capturing PCM frames.")
			state.transcribeArea.SetText(fmt.Sprintf("Stop recording error: %v", err))
		})
		return
	}

	if len(pcmBytes) == 0 {
		fyne.Do(func() {
			state.statusLabel.SetText("No audio captured.")
			state.transcribeArea.SetText("The recording buffer was empty. Please check your microphone.")
		})
		return
	}

	// Trigger the rest of the network and speech workflow in a background goroutine so UI remains fast & responsive
	go func(capturedPCM []byte) {
		tempWav := filepath.Join(os.TempDir(), "yellatmypc_query.wav")
		err = state.recorder.SaveWav(tempWav, capturedPCM)
		if err != nil {
			fyne.Do(func() {
				state.statusLabel.SetText("Error saving WAV file.")
				state.transcribeArea.SetText(fmt.Sprintf("Save WAV error: %v", err))
			})
			return
		}

		fyne.Do(func() {
			state.transcribeArea.SetText(fmt.Sprintf("Audio saved to %s.\nSending base64 audio query to Llama-Server...", tempWav))
		})

		// Post audio completions to llama-server
		reply, err := state.llamaMgr.SendAudioQuery(state.serverConfig, tempWav, state.lastSummary)
		if err != nil {
			fyne.Do(func() {
				state.statusLabel.SetText("Error querying Llama-Server.")
				state.transcribeArea.SetText(fmt.Sprintf("Failed to get response from %s:%s\nError: %v\n\nEnsure llama-server is started and active.", state.serverConfig.Host, state.serverConfig.Port, err))
			})
			return
		}

		actions := ai.ParseActions(reply)
		customVoiceReply := ""
		customVoiceSummary := ""

		if len(actions) > 0 {
			fyne.Do(func() {
				state.transcribeArea.SetText(fmt.Sprintf("Actions parsed successfully: %d actions found.\nExecuting in sequence...", len(actions)))
			})

			go func() {
				for i, act := range actions {
					if act.Tool == "speak_reply" {
						customVoiceReply = act.Reply
						customVoiceSummary = act.Summary
						state.lastSummary = act.Summary

						fyne.Do(func() {
							state.summaryArea.SetText(act.Summary)
							state.transcribeArea.SetText(fmt.Sprintf("%s\n\n[speak_reply summary]: %s", state.transcribeArea.Text, act.Summary))
						})
						continue
					}

					var detail string
					var toolResult string
					var execErr error

					switch act.Tool {
					case "curl_request":
						detail = fmt.Sprintf("HTTP request to %s", act.URL)
						fyne.Do(func() {
							state.transcribeArea.SetText(fmt.Sprintf("%s\n\n[Action %d/%d]: Executing tool 'curl_request' (%s)...", state.transcribeArea.Text, i+1, len(actions), detail))
						})
						toolResult, execErr = performCurlRequest(state.enableCurl, act.Method, act.URL, act.Headers, act.Body)
						if execErr == nil {
							fyne.Do(func() {
								truncated := toolResult
								if len(truncated) > 500 {
									truncated = truncated[:500] + "\n...[truncated]..."
								}
								state.transcribeArea.SetText(fmt.Sprintf("%s\nHTTP Response:\n%s", state.transcribeArea.Text, truncated))
							})
						}
					case "datetime":
						detail = "retrieve date and time"
						fyne.Do(func() {
							state.transcribeArea.SetText(fmt.Sprintf("%s\n\n[Action %d/%d]: Executing tool 'datetime' (%s)...", state.transcribeArea.Text, i+1, len(actions), detail))
						})
						if !state.enableDatetime {
							execErr = fmt.Errorf("datetime tool is disabled in settings")
						} else {
							toolResult = time.Now().Format("2006-01-02 15:04:05 Monday")
							fyne.Do(func() {
								state.transcribeArea.SetText(fmt.Sprintf("%s\nCurrent Date/Time: %s", state.transcribeArea.Text, toolResult))
							})
						}
					default:
						execErr = fmt.Errorf("unsupported tool in dialogue mode: %s", act.Tool)
					}

					if execErr != nil {
						fyne.Do(func() {
							state.transcribeArea.SetText(fmt.Sprintf("%s\nExecution error on action %d (%s): %v", state.transcribeArea.Text, i+1, act.Tool, execErr))
						})
					} else {
						fyne.Do(func() {
							state.transcribeArea.SetText(fmt.Sprintf("%s\nAction %d (%s) executed successfully.", state.transcribeArea.Text, i+1, act.Tool))
						})
					}

					time.Sleep(200 * time.Millisecond)
				}

				// Finalize with speech output
				var speechToPlay string
				if customVoiceReply != "" {
					speechToPlay = customVoiceReply
					if customVoiceSummary != "" {
						fyne.Do(func() {
							state.replyArea.SetText(speechToPlay)
						})
					}
				} else {
					speechToPlay = removeXMLTags(reply)
					fyne.Do(func() {
						state.replyArea.SetText(speechToPlay)
					})
				}

				fyne.Do(func() {
					state.statusLabel.SetText("💬 Playing voice reply...")
				})

				err = tts.Speak(speechToPlay)
				if err != nil {
					fyne.Do(func() {
						state.transcribeArea.SetText(fmt.Sprintf("%s\nTTS Warning: %v", state.transcribeArea.Text, err))
					})
				}

				fyne.Do(func() {
					state.statusLabel.SetText("Idle. Press and Hold to Talk.")
				})
			}()
		} else {
			parsedSummary := ai.ParseXMLTag(reply, "summary")
			parsedReply := ai.ParseXMLTag(reply, "reply")

			var speechToPlay string
			if parsedSummary != "" && parsedReply != "" {
				state.lastSummary = parsedSummary
				speechToPlay = parsedReply
			} else {
				cleanSpeech := removeXMLTags(reply)
				state.lastSummary = fmt.Sprintf("%s\nAI: %s", state.lastSummary, cleanSpeech)
				speechToPlay = cleanSpeech
			}

			fyne.Do(func() {
				state.statusLabel.SetText("💬 Response received! Playing voice...")
				state.replyArea.SetText(speechToPlay)
				state.summaryArea.SetText(state.lastSummary)
				state.transcribeArea.SetText(fmt.Sprintf("Success! Reply fetched from llama-server.\nPassing to system TTS engine..."))
			})

			// Speak response out loud
			err = tts.Speak(speechToPlay)
			if err != nil {
				fyne.Do(func() {
					state.transcribeArea.SetText(fmt.Sprintf("%s\nTTS Warning: %v", state.transcribeArea.Text, err))
				})
			}

			fyne.Do(func() {
				state.statusLabel.SetText("Idle. Press and Hold to Talk.")
			})
		}
	}(pcmBytes)
}

func (state *AppState) setupGlobalHotkey() {
	state.hotkeyMutex.Lock()
	defer state.hotkeyMutex.Unlock()

	// Parse custom config
	keyString := "0x13" // default Pause/Break
	var mods []string
	set, err := ai.LoadSettings()
	if err == nil {
		if set.PTTKeyString != "" {
			keyString = set.PTTKeyString
		}
		mods = set.PTTModifiers
	}

	keyCode := parseKeyCode(keyString, 0x13)
	hkMods := parseModifiers(mods)

	hk := hotkey.New(hkMods, hotkey.Key(keyCode))
	err = hk.Register()
	if err != nil {
		log.Printf("Global Hotkey failed to register: %v. Standard PPT button is still active.", err)
		return
	}

	if state.pttHotkey != nil {
		_ = state.pttHotkey.Unregister()
	}
	state.pttHotkey = hk

	log.Printf("Global PPT Hotkey registered: keycode=%d, modifiers=%v", keyCode, mods)

	go func() {
		for {
			select {
			case <-hk.Keydown():
				state.startRecordingFlow()
			case <-hk.Keyup():
				state.stopRecordingAndProcessFlow()
			}
		}
	}()
}

func parseKeyCode(s string, defaultVal int) int {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return defaultVal
	}
	switch s {
	case "space":
		return 0x20
	case "pause", "break", "pause/break":
		return 0x13
	case "enter", "return":
		return 0x0D
	case "escape", "esc":
		return 0x1B
	}
	var val int
	if strings.HasPrefix(s, "0x") {
		_, err := fmt.Sscanf(s, "0x%x", &val)
		if err == nil {
			return val
		}
	} else {
		_, err := fmt.Sscanf(s, "%d", &val)
		if err == nil {
			return val
		}
	}
	return defaultVal
}

func parseModifiers(mods []string) []hotkey.Modifier {
	var result []hotkey.Modifier
	for _, m := range mods {
		m = strings.ToLower(strings.TrimSpace(m))
		switch m {
		case "ctrl", "control":
			if runtime.GOOS == "windows" {
				result = append(result, hotkey.Modifier(0x02))
			} else {
				result = append(result, hotkey.ModCtrl)
			}
		case "alt", "menu", "option":
			if runtime.GOOS == "windows" {
				result = append(result, hotkey.Modifier(0x01))
			} else if runtime.GOOS == "darwin" {
				result = append(result, hotkey.Modifier(0x02))
			} else {
				result = append(result, hotkey.Modifier(8))
			}
		case "shift":
			if runtime.GOOS == "windows" {
				result = append(result, hotkey.Modifier(0x04))
			} else {
				result = append(result, hotkey.ModShift)
			}
		case "win", "super", "command", "cmd":
			if runtime.GOOS == "windows" {
				result = append(result, hotkey.Modifier(0x08))
			} else if runtime.GOOS == "darwin" {
				result = append(result, hotkey.Modifier(0x08))
			} else {
				result = append(result, hotkey.Modifier(64))
			}
		}
	}
	return result
}

func performCurlRequest(enable bool, method, url string, headers map[string]string, body string) (string, error) {
	if !enable {
		return "", fmt.Errorf("curl actions are disabled in settings")
	}

	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = "GET"
	}

	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to create http request: %w", err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	return string(respBytes), nil
}

func removeXMLTags(text string) string {
	res := text
	startTag := "<action>"
	endTag := "</action>"

	for {
		startIdx := strings.Index(res, startTag)
		if startIdx == -1 {
			break
		}
		endIdx := strings.Index(res, endTag)
		if endIdx == -1 {
			break
		}
		if endIdx > startIdx {
			res = res[:startIdx] + res[endIdx+len(endTag):]
		} else {
			break
		}
	}
	return strings.TrimSpace(res)
}
