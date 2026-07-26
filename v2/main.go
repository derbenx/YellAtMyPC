package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

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
	myApp := app.NewWithID("com.yellatmypc.v2.app")
	myWindow := myApp.NewWindow("YellAtMyPC V2 - AI Computer Agent")
	myWindow.Resize(fyne.NewSize(720, 560))

	recorder, err := audio.NewRecorder()
	if err != nil {
		log.Printf("Warning: Failed to initialize audio recorder: %v", err)
	}

	state := &AppState{
		serverConfig: ai.ServerConfig{
			IsLocal:           true,
			Host:              "127.0.0.1",
			Port:              "8080",
			PersonalityPrompt: "You are a helpful local PC voice assistant with access to computer tools. Respond concisely.",
		},
		llamaMgr:    ai.NewLlamaManager(),
		recorder:    recorder,
		engine:      automation.NewAutomationEngine(),
		loopTracker: automation.NewLoopTracker(),
		win:         myWindow,
	}

	// Setup Personality input direkt on Voice Chat page (6 visible lines)
	state.personalityEntryMain = widget.NewMultiLineEntry()
	state.personalityEntryMain.SetText(state.serverConfig.PersonalityPrompt)
	state.personalityEntryMain.SetMinRowsVisible(6)
	state.personalityEntryMain.Wrapping = fyne.TextWrapWord // Word wrap enabled!
	state.personalityEntryMain.OnChanged = func(text string) {
		state.serverConfig.PersonalityPrompt = text
	}

	// Build Tab 1: Main Push To Talk / Chat Automation Tab
	state.statusLabel = widget.NewLabel("Idle. Press and Hold Button or Win+Space to Talk.")
	state.statusLabel.Alignment = fyne.TextAlignCenter
	state.statusLabel.TextStyle = fyne.TextStyle{Bold: true}

	state.transcribeArea = newReadOnlyEntry()
	state.transcribeArea.SetPlaceHolder("Current status details and computer automation execution logs...")
	state.transcribeArea.SetMinRowsVisible(4) // 4 lines visible height

	state.replyArea = newReadOnlyEntry()
	state.replyArea.SetPlaceHolder("AI spoken reply will appear here...")

	state.summaryArea = newReadOnlyEntry()
	state.summaryArea.SetPlaceHolder("AI memory summary of PC state and conversation context...")

	// Green Custom Hold Button
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
			state.transcribeArea.SetText("Conversation history and memory context cleared.")
		})
		_ = tts.Speak("Conversation history cleared.")
	})

	// Clean 50/50 grid split for Voice Chat tab
	leftSide := container.NewVBox(
		widget.NewLabelWithStyle("AI Voice Controls", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("AI Agent System & Personality Prompt:"),
		state.personalityEntryMain,
		clearHistoryBtn,
		state.statusLabel, // Status label right above the green PTT button
		holdButton,
		widget.NewLabel("System Status Log:"),
		container.NewGridWrap(fyne.NewSize(330, 110), state.transcribeArea), // ~4 lines tall
	)

	rightSide := container.NewVBox(
		widget.NewLabel("AI Memory & Conversation Summary:"),
		container.NewGridWrap(fyne.NewSize(330, 160), state.summaryArea),
		widget.NewLabel("AI Agent Spoken Reply Text:"),
		container.NewGridWrap(fyne.NewSize(330, 160), state.replyArea),
	)

	mainTabContent := container.NewBorder(
		nil, nil, nil, nil,
		container.NewGridWithColumns(2, leftSide, rightSide),
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

	// Microphone select dropdown on setup
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
		container.NewTabItemWithIcon("Safety Checklist", theme.ConfirmIcon(), safetyTabContent),
		container.NewTabItemWithIcon("Allowed Apps", theme.FileApplicationIcon(), allowlistTabContent),
		container.NewTabItemWithIcon("Setup / Config", theme.SettingsIcon(), configTabContent),
	)

	myWindow.SetContent(tabs)

	// Scan folders and microphones on startup
	state.scanFiles()
	state.refreshMicrophones()

	// Load persistent settings if available
	state.loadPersistentSettings()

	// Setup Global Hotkeys on background threads
	go state.setupGlobalHotkey()
	go state.setupBluetoothMediaHotkey()
	go state.setupEmergencyStopHotkey()

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

		// Post audio completions to llama-server (passing the last running context summary)
		reply, err := state.llamaMgr.SendAudioQuery(state.serverConfig, tempWav, state.lastSummary)
		if err != nil {
			fyne.Do(func() {
				state.statusLabel.SetText("Error querying Llama-Server.")
				state.transcribeArea.SetText(fmt.Sprintf("Failed to get response from %s:%s\nError: %v\n\nEnsure llama-server is started and active.", state.serverConfig.Host, state.serverConfig.Port, err))
			})
			return
		}

		fyne.Do(func() {
			state.statusLabel.SetText("💬 Response received! Processing reply...")
			state.replyArea.SetText(reply)
		})

		// Parse XML actions
		actions := ai.ParseActions(reply)
		customVoiceReply := ""
		customVoiceSummary := ""

		if len(actions) > 0 {
			fyne.Do(func() {
				state.transcribeArea.SetText(fmt.Sprintf("Actions parsed successfully: %d actions found.\nExecuting in sequence...", len(actions)))
			})

			// Sequential execution of tool actions
			go func() {
				for i, act := range actions {
					if act.Tool == "speak_reply" {
						customVoiceReply = act.Reply
						customVoiceSummary = act.Summary

						// Store this updated summary into our context memory so it's passed on the next message
						state.lastSummary = act.Summary

						fyne.Do(func() {
							state.summaryArea.SetText(act.Summary)
							state.transcribeArea.SetText(fmt.Sprintf("%s\n\n[speak_reply summary]: %s", state.transcribeArea.Text, act.Summary))
						})
						continue
					}

					// Loop prevention check
					argsString := fmt.Sprintf("%s:%v:%v:%s", act.Text, act.X, act.Y, act.Name)
					_, alert, breakConnection := state.loopTracker.TrackAction(act.Tool, argsString)

					if alert != "" {
						fyne.Do(func() {
							state.transcribeArea.SetText(fmt.Sprintf("%s\n%s", state.transcribeArea.Text, alert))
						})
						_ = tts.Speak(alert)
					}

					if breakConnection {
						fyne.Do(func() {
							state.transcribeArea.SetText(fmt.Sprintf("%s\nBreaking active connection due to duplicate infinite loop safety trigger.", state.transcribeArea.Text))
						})
						break
					}

					fyne.Do(func() {
						state.transcribeArea.SetText(fmt.Sprintf("%s\n\n[Action %d/%d]: Executing tool '%s'...", state.transcribeArea.Text, i+1, len(actions), act.Tool))
					})

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
							fyne.Do(func() {
								state.transcribeArea.SetText(fmt.Sprintf("%s\nScreenshot captured successfully to %s", state.transcribeArea.Text, scrFile))
							})
						}
					case "read_selection":
						var text string
						text, execErr = state.engine.ReadSelection()
						if execErr == nil {
							fyne.Do(func() {
								state.transcribeArea.SetText(fmt.Sprintf("%s\nCopied Selection Content: %s", state.transcribeArea.Text, text))
							})
						}
					default:
						execErr = fmt.Errorf("unknown or unsupported action tool: %s", act.Tool)
					}

					if execErr != nil {
						fyne.Do(func() {
							state.transcribeArea.SetText(fmt.Sprintf("%s\nExecution error on action %d: %v", state.transcribeArea.Text, i+1, execErr))
						})
					} else {
						fyne.Do(func() {
							state.transcribeArea.SetText(fmt.Sprintf("%s\nAction %d executed successfully.", state.transcribeArea.Text, i+1))
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
			fyne.Do(func() {
				state.transcribeArea.SetText("No computer tool calls parsed in response. Normal dialogue flow.")
			})
			cleanSpeech := removeXMLTags(reply)

			// Store the reply as the summary fallback context if tool wasn't invoked
			state.lastSummary = fmt.Sprintf("%s\nAI: %s", state.lastSummary, cleanSpeech)

			fyne.Do(func() {
				state.summaryArea.SetText(state.lastSummary)
				state.replyArea.SetText(cleanSpeech)
				state.statusLabel.SetText("💬 Playing voice reply...")
			})
			err = tts.Speak(cleanSpeech)
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

func (state *AppState) setupEmergencyStopHotkey() {
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
