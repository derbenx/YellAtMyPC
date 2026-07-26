package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"YellAtMyPC/v2/ai"
	"YellAtMyPC/v2/audio"
	"YellAtMyPC/v2/automation"
	"YellAtMyPC/v2/tts"

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

	// Automation and Safety checklist
	engine      *automation.AutomationEngine
	loopTracker *automation.LoopTracker

	// Safety Checklist Controls
	mouseCheck  *widget.Check
	keysCheck   *widget.Check
	screenCheck *widget.Check
	appsCheck   *widget.Check

	// App Allowlist GUI Controls
	allowlistContainer *fyne.Container
	newAppNameEntry    *widget.Entry
	newAppPathEntry    *widget.Entry

	// Personality Inputs (synchronized)
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
	myApp := app.NewWithID("com.yellatmypc.v2.app")
	myWindow := myApp.NewWindow("YellAtMyPC V2 - AI Computer Agent")
	myWindow.Resize(fyne.NewSize(700, 560))

	recorder, err := audio.NewRecorder()
	if err != nil {
		log.Printf("Warning: Failed to initialize audio recorder: %v", err)
	}

	state := &AppState{
		serverConfig: ai.ServerConfig{
			IsLocal:        true,
			Host:           "127.0.0.1",
			Port:           "8080",
			PersonalityPrompt: "You are a helpful local PC voice assistant with access to computer tools. Respond concisely.",
		},
		llamaMgr:    ai.NewLlamaManager(),
		recorder:    recorder,
		engine:      automation.NewAutomationEngine(),
		loopTracker: automation.NewLoopTracker(),
		win:         myWindow,
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

	// Build Tab 1: Main Push To Talk / Chat Automation Tab
	state.statusLabel = widget.NewLabel("Idle. Press and Hold Button or Win+Space to Talk.")
	state.statusLabel.Alignment = fyne.TextAlignCenter
	state.statusLabel.TextStyle = fyne.TextStyle{Bold: true}

	state.transcribeArea = widget.NewMultiLineEntry()
	state.transcribeArea.SetPlaceHolder("Current status details and computer automation execution logs...")
	state.transcribeArea.Disable()

	state.replyArea = widget.NewMultiLineEntry()
	state.replyArea.SetPlaceHolder("AI reply and parsed JSON XML actions will appear here...")
	state.replyArea.Disable()

	holdButton := newHoldButton("Push & Hold to Talk", func() {
		state.startRecordingFlow()
	}, func() {
		state.stopRecordingAndProcessFlow()
	})

	mainTabContent := container.NewVBox(
		widget.NewCard("Voice Assistant & Automation Agent", "Press and hold to talk, release to execute PC actions",
			container.NewVBox(
				state.statusLabel,
				widget.NewLabel("AI Agent System & Personality Prompt:"),
				state.personalityEntryMain,
				container.NewGridWithColumns(2,
					container.NewVBox(widget.NewLabel("System Automation Logs:"), container.NewGridWrap(fyne.NewSize(310, 150), state.transcribeArea)),
					container.NewVBox(widget.NewLabel("AI Agent Reply & Tool Outputs:"), container.NewGridWrap(fyne.NewSize(310, 150), state.replyArea)),
				),
				holdButton,
			),
		),
	)

	// Build Tab 2: Safety Checklist Tab
	state.mouseCheck = widget.NewCheck("Enable Mouse Actions (Smooth Movement, Clicking)", func(checked bool) {
		state.engine.EnableMouse = checked
	})
	state.mouseCheck.SetChecked(true)

	state.keysCheck = widget.NewCheck("Enable Keyboard Actions (Typing strings, Special keys, Copy selection)", func(checked bool) {
		state.engine.EnableKeys = checked
	})
	state.keysCheck.SetChecked(true)

	state.screenCheck = widget.NewCheck("Enable Screenshot Capture (Allows the AI to 'see' the screen coordinates)", func(checked bool) {
		state.engine.EnableScreen = checked
	})
	state.screenCheck.SetChecked(true)

	state.appsCheck = widget.NewCheck("Enable Launching Apps (Allow execution of files in your whitelist)", func(checked bool) {
		state.engine.EnableApps = checked
	})
	state.appsCheck.SetChecked(true)

	safetyTabContent := container.NewVBox(
		widget.NewCard("Safety Gate & Checklist", "Restrict what the AI agent can execute autonomously",
			container.NewVBox(
				widget.NewLabelWithStyle("Enable or disable specific hardware tools globally:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
				state.mouseCheck,
				state.keysCheck,
				state.screenCheck,
				state.appsCheck,
				widget.NewSeparator(),
				widget.NewLabelWithStyle("Emergency Kill Switch: Win+Z", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
				widget.NewLabel("Pressing the global 'Win+Z' hotkey anytime instantly terminates the YellAtMyPC process and stops the llama-server background loop!"),
			),
		),
	)

	// Build Tab 3: App Allowlist Configuration Tab
	state.allowlistContainer = container.NewVBox()
	state.refreshAllowlistGUI()

	state.newAppNameEntry = widget.NewEntry()
	state.newAppNameEntry.SetPlaceHolder("App Alias (e.g. notepad)")
	state.newAppPathEntry = widget.NewEntry()
	state.newAppPathEntry.SetPlaceHolder("Executable Path (e.g. notepad.exe)")

	addAppBtn := widget.NewButtonWithIcon("Add To Allowlist", theme.ContentAddIcon(), func() {
		name := state.newAppNameEntry.Text
		path := state.newAppPathEntry.Text
		if name == "" || path == "" {
			dialog.ShowError(fmt.Errorf("please supply both app alias name and executable path"), state.win)
			return
		}
		state.engine.AllowedApps.SetAllowed(name, path)
		state.newAppNameEntry.SetText("")
		state.newAppPathEntry.SetText("")
		state.refreshAllowlistGUI()
	})

	allowlistTabContent := container.NewVScroll(container.NewVBox(
		widget.NewCard("Allowed Applications List", "The AI can only launch files mapped in this whitelist for security",
			container.NewVBox(
				state.allowlistContainer,
				widget.NewSeparator(),
				widget.NewLabelWithStyle("Register New Executable Mapping:", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
				container.NewGridWithColumns(2,
					container.NewVBox(widget.NewLabel("Name Alias:"), state.newAppNameEntry),
					container.NewVBox(widget.NewLabel("Executable Path / Command:"), state.newAppPathEntry),
				),
				container.NewHBox(layout.NewSpacer(), addAppBtn),
			),
		),
	))

	// Build Tab 4: Setup / Config Tab
	state.serverStatus = widget.NewLabel("Local Server Status: Stopped")

	state.localRadio = widget.NewRadioGroup([]string{"Local Server (Self-Hosted)", "Network / Remote Server"}, func(selected string) {
		if selected == "Local Server (Self-Hosted)" {
			state.serverConfig.IsLocal = true
			state.hostEntry.SetText("127.0.0.1")
			state.hostEntry.Disable()
			state.ggufSelect.Enable()
			state.mmprojSelect.Enable()
			state.llamaSelect.Enable()
			state.launchBtn.Enable()
		} else {
			state.serverConfig.IsLocal = false
			state.hostEntry.Enable()
			state.ggufSelect.Disable()
			state.mmprojSelect.Disable()
			state.llamaSelect.Disable()
			state.launchBtn.Disable()
		}
	})
	state.localRadio.SetSelected("Local Server (Self-Hosted)")

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
		container.NewTabItemWithIcon("Safety Checklist", theme.ConfirmIcon(), safetyTabContent),
		container.NewTabItemWithIcon("Allowed Apps", theme.FileApplicationIcon(), allowlistTabContent),
		container.NewTabItemWithIcon("Setup / Config", theme.SettingsIcon(), configTabContent),
	)

	myWindow.SetContent(tabs)

	// Scan folders on startup
	state.scanFiles()

	// Setup Global Hotkeys on background threads
	go state.setupGlobalHotkey()
	go state.setupEmergencyStopHotkey()

	myWindow.SetOnClosed(func() {
		if state.recorder != nil {
			state.recorder.Close()
		}
		_ = state.llamaMgr.StopServer()
	})

	myWindow.ShowAndRun()
}

func (state *AppState) refreshAllowlistGUI() {
	state.allowlistContainer.Objects = nil
	list := state.engine.AllowedApps.GetList()

	if len(list) == 0 {
		state.allowlistContainer.Add(widget.NewLabel("No apps currently whitelisted."))
		state.allowlistContainer.Refresh()
		return
	}

	for k, v := range list {
		appKey := k // capture loop var
		lbl := widget.NewLabel(fmt.Sprintf("%s  ->  %s", k, v))
		btn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
			state.engine.AllowedApps.RemoveAllowed(appKey)
			state.refreshAllowlistGUI()
		})
		row := container.NewHBox(lbl, layout.NewSpacer(), btn)
		state.allowlistContainer.Add(row)
	}
	state.allowlistContainer.Refresh()
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

		state.statusLabel.SetText("💬 Response received! Speaking reply...")
		state.replyArea.SetText(reply)

		// Parse XML actions
		actions := ai.ParseActions(reply)
		if len(actions) > 0 {
			state.transcribeArea.SetText(fmt.Sprintf("Actions parsed successfully: %d actions found.\nExecuting in sequence...", len(actions)))

			// Sequential execution of tool actions
			go func() {
				for i, act := range actions {
					// Loop prevention check
					argsString := fmt.Sprintf("%s:%v:%v:%s", act.Text, act.X, act.Y, act.Name)
					_, alert, breakConnection := state.loopTracker.TrackAction(act.Tool, argsString)

					if alert != "" {
						state.transcribeArea.SetText(fmt.Sprintf("%s\n%s", state.transcribeArea.Text, alert))
						_ = tts.Speak(alert)
					}

					if breakConnection {
						state.transcribeArea.SetText(fmt.Sprintf("%s\nBreaking active connection due to duplicate infinite loop safety trigger.", state.transcribeArea.Text))
						break
					}

					state.transcribeArea.SetText(fmt.Sprintf("%s\n\n[Action %d/%d]: Executing tool '%s'...", state.transcribeArea.Text, i+1, len(actions), act.Tool))

					var execErr error
					switch act.Tool {
					case "type_text":
						execErr = state.engine.TypeText(act.Text)
					case "press_key":
						execErr = state.engine.PressKey(act.Key, act.Modifiers)
					case "click_mouse":
						execErr = state.engine.ClickMouse(act.Button, act.Double)
					case "move_mouse":
						execErr = state.engine.MoveMouse(act.X, act.Y)
					case "run_app":
						execErr = state.engine.RunApp(act.Name)
					case "take_screenshot":
						scrFile := filepath.Join(os.TempDir(), "yellatmypc_screenshot.png")
						execErr = state.engine.TakeScreenshot(scrFile)
						if execErr == nil {
							state.transcribeArea.SetText(fmt.Sprintf("%s\nScreenshot captured successfully to %s", state.transcribeArea.Text, scrFile))
						}
					case "read_selection":
						var text string
						text, execErr = state.engine.ReadSelection()
						if execErr == nil {
							state.transcribeArea.SetText(fmt.Sprintf("%s\nCopied Selection Content: %s", state.transcribeArea.Text, text))
						}
					default:
						execErr = fmt.Errorf("unknown or unsupported action tool: %s", act.Tool)
					}

					if execErr != nil {
						state.transcribeArea.SetText(fmt.Sprintf("%s\nExecution error on action %d: %v", state.transcribeArea.Text, i+1, execErr))
					} else {
						state.transcribeArea.SetText(fmt.Sprintf("%s\nAction %d executed successfully.", state.transcribeArea.Text, i+1))
					}

					// Small wait between sequential actions
					time.Sleep(200 * time.Millisecond)
				}
			}()
		} else {
			state.transcribeArea.SetText("No computer tool calls parsed in response. Normal dialogue flow.")
		}

		// Speak reply text out loud (ignoring the raw JSON xml tags for clean speech!)
		cleanSpeech := removeXMLTags(reply)
		err = tts.Speak(cleanSpeech)
		if err != nil {
			state.transcribeArea.SetText(fmt.Sprintf("TTS Warning: %v", err))
		}

		state.statusLabel.SetText("Idle. Press and Hold to Talk.")
	}(pcmBytes)
}

// Clean up spoken voice output by stripping out JSON xml action strings
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

func (state *AppState) setupEmergencyStopHotkey() {
	// Register emergency stop hotkey: Win + Z (ModWin + KeyZ)
	// We map modifier 0x08 (ModWin) and KeyZ is 0x005a (standard KeyZ code)
	// Let's use hotkey.KeyZ if defined or standard uint32 virtual key. KeyZ code is 0x5a.
	hkStop := hotkey.New([]hotkey.Modifier{hotkey.Modifier(0x08)}, hotkey.Key(0x5a))
	err := hkStop.Register()
	if err != nil {
		log.Printf("Emergency Hotkey Win+Z failed to register: %v", err)
		return
	}

	log.Println("Emergency Kill Switch active: Press Win+Z anytime to immediately close application.")

	for {
		select {
		case <-hkStop.Keydown():
			log.Println("⚠️ EMERGENCY STOP TRIGGERED! Stopping llama-server and exiting...")
			if state.recorder != nil {
				state.recorder.Close()
			}
			_ = state.llamaMgr.StopServer()
			os.Exit(0)
		}
	}
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
