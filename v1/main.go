package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

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

type AppState struct {
	serverConfig    ai.ServerConfig
	llamaMgr        *ai.LlamaManager
	recorder        *audio.Recorder
	isRecording     bool
	recordingMutex  sync.Mutex
	statusLabel     *widget.Label
	transcribeArea  *widget.Entry
	replyArea       *widget.Entry
	serverStatus    *widget.Label
	win             fyne.Window

	// Personality Inputs (synchronized between both pages)
	personalityEntryMain  *widget.Entry
	personalityEntrySetup *widget.Entry

	// Setup controls
	localRadio      *widget.RadioGroup
	hostEntry       *widget.Entry
	portEntry       *widget.Entry
	ggufSelect      *widget.Select
	mmprojSelect    *widget.Select
	llamaSelect     *widget.Select
	launchBtn       *widget.Button
	saveBtn         *widget.Button
}

func main() {
	myApp := app.NewWithID("com.yellatmypc.app")
	myWindow := myApp.NewWindow("YellAtMyPC - Push To Talk AI Assistant")
	myWindow.Resize(fyne.NewSize(650, 520))

	recorder, err := audio.NewRecorder()
	if err != nil {
		log.Printf("Warning: Failed to initialize audio recorder: %v", err)
	}

	state := &AppState{
		serverConfig: ai.ServerConfig{
			IsLocal:        true,
			Host:           "127.0.0.1",
			Port:           "8080",
			PersonalityPrompt: "You are a helpful local PC voice assistant. Respond concisely to the spoken audio.",
		},
		llamaMgr: ai.NewLlamaManager(),
		recorder: recorder,
		win:      myWindow,
	}

	// Synchronize personality inputs
	state.personalityEntryMain = widget.NewEntry()
	state.personalityEntryMain.SetText(state.serverConfig.PersonalityPrompt)
	state.personalityEntryMain.OnChanged = func(text string) {
		state.serverConfig.PersonalityPrompt = text
		if state.personalityEntrySetup.Text != text {
			state.personalityEntrySetup.SetText(text)
		}
	}

	state.personalityEntrySetup = widget.NewEntry()
	state.personalityEntrySetup.SetText(state.serverConfig.PersonalityPrompt)
	state.personalityEntrySetup.OnChanged = func(text string) {
		state.serverConfig.PersonalityPrompt = text
		if state.personalityEntryMain.Text != text {
			state.personalityEntryMain.SetText(text)
		}
	}

	// Build Tab 1: Main Push To Talk Tab (with standard, non-deprecated widget.NewLabel)
	state.statusLabel = widget.NewLabel("Idle. Press and Hold Button or Win+Space to Talk.")
	state.statusLabel.Alignment = fyne.TextAlignCenter
	state.statusLabel.TextStyle = fyne.TextStyle{Bold: true}

	state.transcribeArea = widget.NewMultiLineEntry()
	state.transcribeArea.SetPlaceHolder("Current status details and process logs...")
	state.transcribeArea.Disable()

	state.replyArea = widget.NewMultiLineEntry()
	state.replyArea.SetPlaceHolder("AI reply will appear here...")
	state.replyArea.Disable()

	// Create our custom press-and-hold button widget
	holdButton := newHoldButton("Push & Hold to Talk", func() {
		state.startRecordingFlow()
	}, func() {
		state.stopRecordingAndProcessFlow()
	})

	mainTabContent := container.NewVBox(
		widget.NewCard("Talk to your AI", "Speak clearly into your microphone",
			container.NewVBox(
				state.statusLabel,
				widget.NewLabel("AI Personality Prompt:"),
				state.personalityEntryMain,
				container.NewGridWithColumns(2,
					container.NewVBox(widget.NewLabel("Status log:"), container.NewGridWrap(fyne.NewSize(280, 140), state.transcribeArea)),
					container.NewVBox(widget.NewLabel("AI Reply:"), container.NewGridWrap(fyne.NewSize(280, 140), state.replyArea)),
				),
				holdButton,
			),
		),
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
	})

	configTabContent := container.NewVScroll(container.NewVBox(
		widget.NewCard("Connection Type", "", state.localRadio),
		container.NewGridWithColumns(2,
			container.NewVBox(widget.NewLabel("Host IP:"), state.hostEntry),
			container.NewVBox(widget.NewLabel("Port:"), state.portEntry),
		),
		widget.NewLabel("Personality:"),
		state.personalityEntrySetup,
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

	// Scan folders on startup
	state.scanFiles()

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

// Custom HoldButton to support press & hold callbacks
type holdButton struct {
	widget.BaseWidget
	text      string
	onPress   func()
	onRelease func()
}

func newHoldButton(text string, onPress, onRelease func()) *holdButton {
	b := &holdButton{
		text:      text,
		onPress:   onPress,
		onRelease: onRelease,
	}
	b.ExtendBaseWidget(b)
	return b
}

func (b *holdButton) CreateRenderer() fyne.WidgetRenderer {
	lbl := widget.NewLabel(b.text)
	lbl.Alignment = fyne.TextAlignCenter
	lbl.TextStyle = fyne.TextStyle{Bold: true}
	bg := widget.NewCard("", "", lbl)
	return &holdButtonRenderer{
		button: b,
		lbl:    lbl,
		bg:     bg,
	}
}

type holdButtonRenderer struct {
	button *holdButton
	lbl    *widget.Label
	bg     *widget.Card
}

func (r *holdButtonRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
}

func (r *holdButtonRenderer) MinSize() fyne.Size {
	return fyne.NewSize(120, 50)
}

func (r *holdButtonRenderer) Refresh() {
	r.lbl.SetText(r.button.text)
	r.bg.Refresh()
}

func (r *holdButtonRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg}
}

func (r *holdButtonRenderer) Destroy() {}

// Implement touch & mouse interfaces for multi-platform hold-to-talk button
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

func (state *AppState) saveConfiguration() {
	state.serverConfig.Host = state.hostEntry.Text
	state.serverConfig.Port = state.portEntry.Text
	state.serverConfig.PersonalityPrompt = state.personalityEntrySetup.Text

	if state.serverConfig.IsLocal {
		state.serverConfig.GgufFile = state.ggufSelect.Selected
		state.serverConfig.MmprojFile = state.mmprojSelect.Selected
	}

	dialog.ShowInformation("Configuration Saved", "The app setup was updated successfully.", state.win)
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
	state.statusLabel.SetText("🎤 Recording... Release when finished speaking.")
	state.transcribeArea.SetText("Capturing audio frames...")

	if state.recorder != nil {
		err := state.recorder.Start()
		if err != nil {
			state.isRecording = false
			state.statusLabel.SetText("Error starting microphone.")
			state.transcribeArea.SetText(fmt.Sprintf("Microphone error: %v", err))
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
	state.statusLabel.SetText("⌛ Stopped. Processing audio & querying AI...")

	if state.recorder == nil {
		state.statusLabel.SetText("No active microphone available.")
		return
	}

	// SYNCHRONOUSLY capture and stop the recorder to avoid any concurrent race conditions
	pcmBytes, err := state.recorder.Stop()
	if err != nil {
		state.statusLabel.SetText("Error capturing PCM frames.")
		state.transcribeArea.SetText(fmt.Sprintf("Stop recording error: %v", err))
		return
	}

	if len(pcmBytes) == 0 {
		state.statusLabel.SetText("No audio captured.")
		state.transcribeArea.SetText("The recording buffer was empty. Please check your microphone.")
		return
	}

	// Trigger the rest of the network and speech workflow in a background goroutine so UI remains fast & responsive
	go func(capturedPCM []byte) {
		tempWav := filepath.Join(os.TempDir(), "yellatmypc_query.wav")
		err = state.recorder.SaveWav(tempWav, capturedPCM)
		if err != nil {
			state.statusLabel.SetText("Error saving WAV file.")
			state.transcribeArea.SetText(fmt.Sprintf("Save WAV error: %v", err))
			return
		}

		state.transcribeArea.SetText(fmt.Sprintf("Audio saved to %s.\nSending base64 audio query to Llama-Server...", tempWav))

		// Post audio completions to llama-server
		reply, err := state.llamaMgr.SendAudioQuery(state.serverConfig, tempWav)
		if err != nil {
			state.statusLabel.SetText("Error querying Llama-Server.")
			state.transcribeArea.SetText(fmt.Sprintf("Failed to get response from %s:%s\nError: %v\n\nEnsure llama-server is started and active.", state.serverConfig.Host, state.serverConfig.Port, err))
			return
		}

		state.statusLabel.SetText("💬 Response received! Playing voice...")
		state.replyArea.SetText(reply)
		state.transcribeArea.SetText(fmt.Sprintf("Success! Reply fetched from llama-server in standard chat completion format.\nPassing to system TTS engine..."))

		// Speak response out loud
		err = tts.Speak(reply)
		if err != nil {
			state.transcribeArea.SetText(fmt.Sprintf("TTS Warning: %v", err))
		}

		state.statusLabel.SetText("Idle. Press and Hold to Talk.")
	}(pcmBytes)
}

func (state *AppState) setupGlobalHotkey() {
	// Register global hotkey: Win + Space
	// Note: Win + Space requires ModWin on windows, and is a modifier value of 0x8.
	// Since gobuild on Linux won't have hotkey.ModWin inside the public API if it's windows-only,
	// let's use the constant 0x08 for ModWin. Or we can use ModAlt or similar if ModWin is undefined.
	// We'll define a custom modifier slice or use ModAlt/ModCtrl.
	// In golang.design/x/hotkey, on Darwin, Windows and Linux, ModWin is defined. Let's cast it safely:
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
