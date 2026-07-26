package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
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
	}

	// Build Tab 1: Main Push To Talk Tab (with standard, non-deprecated widget.NewLabel)
	state.statusLabel = widget.NewLabel("Idle. Press and Hold Button or Win+Space to Talk.")
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
		fyne.Do(func() {
			state.summaryArea.SetText("")
			state.replyArea.SetText("")
			state.transcribeArea.SetText("Conversation history cleared.")
		})
		_ = tts.Speak("Conversation history cleared.")
	})

	// Layout the Voice Chat page in a clean 50/50 Left/Right Dashboard split
	leftSide := container.NewVBox(
		widget.NewLabelWithStyle("AI Voice Controls", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("AI Personality Prompt:"),
		state.personalityEntryMain,
		clearHistoryBtn,
		state.statusLabel, // Idle Status right above the green PTT button
		holdButton,
		widget.NewLabel("System Status Log:"),
		container.NewGridWrap(fyne.NewSize(320, 110), state.transcribeArea), // ~4 lines tall
	)

	rightSide := container.NewVBox(
		widget.NewLabel("AI Memory & Conversation Summary:"),
		container.NewGridWrap(fyne.NewSize(320, 160), state.summaryArea),
		widget.NewLabel("AI Spoken Reply Text:"),
		container.NewGridWrap(fyne.NewSize(320, 160), state.replyArea),
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
		container.NewHBox(layout.NewSpacer(), state.saveBtn),
	))

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("Voice Chat", theme.HomeIcon(), mainTabContent),
		container.NewTabItemWithIcon("Setup / Config", theme.SettingsIcon(), configTabContent),
	)

	myWindow.SetContent(tabs)

	// Scan folders and microphones on startup
	state.scanFiles()
	state.refreshMicrophones()

	// Load persistent settings if available
	state.loadPersistentSettings()

	// Setup Global Hotkey and Bluetooth Media Button on background threads
	go state.setupGlobalHotkey()
	go state.setupBluetoothMediaHotkey()

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
	})
	log.Println("Successfully loaded persistent settings from settings.json")
}

func (state *AppState) saveConfiguration() {
	state.serverConfig.Host = state.hostEntry.Text
	state.serverConfig.Port = state.portEntry.Text
	state.serverConfig.PersonalityPrompt = state.personalityEntryMain.Text

	if state.serverConfig.IsLocal {
		state.serverConfig.GgufFile = state.ggufSelect.Selected
		state.serverConfig.MmprojFile = state.mmprojSelect.Selected
	}

	// Write to shared settings file
	settings := &ai.AppSettings{
		IsLocal:           state.serverConfig.IsLocal,
		Host:              state.hostEntry.Text,
		Port:              state.portEntry.Text,
		GgufFile:          state.ggufSelect.Selected,
		MmprojFile:        state.mmprojSelect.Selected,
		LlamaFile:         state.llamaSelect.Selected,
		PersonalityPrompt: state.personalityEntryMain.Text,
		MicrophoneName:    state.micSelect.Selected,
	}

	err := ai.SaveSettings(settings)
	if err != nil {
		dialog.ShowError(fmt.Errorf("failed to save settings.json: %v", err), state.win)
		return
	}

	dialog.ShowInformation("Configuration Saved", "Setup configurations saved successfully to settings.json.", state.win)
}

func (state *AppState) refreshMicrophones() {
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

	fyne.Do(func() {
		state.micSelect.Options = options
		if len(options) > 0 {
			state.micSelect.SetSelected(options[0])
		}
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

func (state *AppState) scanFiles() {
	servers, ggufs, mmprojs := ai.FindLocalFiles()

	fyne.Do(func() {
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

		// Parse XML custom summary and reply tags for dialogue memory context
		parsedSummary := ai.ParseXMLTag(reply, "summary")
		parsedReply := ai.ParseXMLTag(reply, "reply")

		var speechToPlay string
		if parsedSummary != "" && parsedReply != "" {
			state.lastSummary = parsedSummary
			speechToPlay = parsedReply
		} else {
			// Fallback if model output is direct text
			state.lastSummary = fmt.Sprintf("%s\nAI: %s", state.lastSummary, reply)
			speechToPlay = reply
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
	}(pcmBytes)
}

func (state *AppState) setupGlobalHotkey() {
	// Register global hotkey: Win + Space
	hk := hotkey.New([]hotkey.Modifier{hotkey.Modifier(0x08)}, hotkey.KeySpace)
	err := hk.Register()
	if err != nil {
		log.Printf("Global Hotkey registration failed (perhaps Win+Space is restricted on this machine): %v. Standard PPT button is still fully active.", err)
		return
	}

	log.Println("Global PPT Hotkey registered: Win + Space")

	for {
		select {
		case <-hk.Keydown():
			state.startRecordingFlow()
		case <-hk.Keyup():
			state.stopRecordingAndProcessFlow()
		}
	}
}

func (state *AppState) setupBluetoothMediaHotkey() {
	// Register Bluetooth PTT toggle key: VK_MEDIA_PLAY_PAUSE (code 0xB3)
	// We do not require modifiers so they can just press play/pause on hands-free headsets/car-kits
	hkMedia := hotkey.New(nil, hotkey.Key(0xB3))
	err := hkMedia.Register()
	if err != nil {
		log.Printf("Bluetooth Media Hotkey registration skipped (or not supported on this host): %v", err)
		return
	}

	log.Println("Bluetooth PTT Handset Toggle registered! Tap your Jabra Play/Pause key to Start/Stop speaking.")

	for {
		select {
		case <-hkMedia.Keydown():
			// Use it as a pure toggle switch: if recording is off, start. If recording is on, stop and submit!
			state.recordingMutex.Lock()
			recordingActive := state.isRecording
			state.recordingMutex.Unlock()

			if !recordingActive {
				log.Println("Bluetooth Media button tapped: Starting recording...")
				state.startRecordingFlow()
			} else {
				log.Println("Bluetooth Media button tapped: Stopping recording & submitting query...")
				state.stopRecordingAndProcessFlow()
			}
		}
	}
}
