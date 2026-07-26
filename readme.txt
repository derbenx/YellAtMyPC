YellAtMyPC is a PPT (Push To Talk) program written in GO that lets you actually talk to llama cpp and your AI agent of choice. Then the output is sent back and passed to TTS (Text to Speech), so it can talk back.
Gemma E4B GGUF is recommended. Gemma understands audio natively. 

Requirements:
 microphone and PC attached to microphone.
 llama cpp
 Gemma E4B GGUF + mproj
 This app, YellAtMyPC

Make a folder called YellAtMyPC
Place llama in a subfolder called llama.
Put gemma and the mproj in a folder called ai.
Paste YellAtMyPC in the main folder with the llama and ai.

->YellAtMyPC
--> ai
--> llama
--> v1 (Simple Chatting back and forth)
--> v2 (Computer Agent Control with allowed lists, loop tracking, safety gates)
--> YellAtMyPC.exe

V1 is just chatting back and forth.
V2 lets you ask the AI to do things: control mouse, click, type, copy selections, and launch permitted apps!

========================
How to Build and Compile
========================
To build the application, Fyne requires graphics hardware integration. CGo must be enabled, and you must use your portable MinGW GCC toolchain.

To build V1 (Simple Voice Chat):
  1. Open your terminal using your portable Go `.bat` script to load your environment variables.
  2. Run:
       go build -o YellAtMyPC_V1.exe ./v1

To build V2 (Local AI Computer Agent):
  1. Open your terminal using your portable Go `.bat` script.
  2. Run:
       go build -o YellAtMyPC_V2.exe ./v2

On Linux (V1 / V2):
  1. Install development packages:
       sudo apt-get install -y libgl1-mesa-dev xorg-dev libegl1-mesa-dev libwayland-dev libxkbcommon-dev
  2. Run:
       CGO_ENABLED=1 go build -o yell_v2 ./v2

==============================
How to Run & Configure (V2)
==============================
1. Open YellAtMyPC_V2.exe.
2. In the "Setup / Config" tab:
   - Select either "Local Server" or "Network / Remote Server".
   - Click "Scan relative directories" to auto-populate files.
   - Select your GGUF model, multimodal mproj file, and Llama server executable.
   - Click "Launch Local Llama" to start llama-server.
3. In the "Safety Checklist" tab:
   - Restrict AI permissions (Mouse, Keyboard, Screenshot, Launch Apps) anytime using simple checkboxes.
   - Emergency Stop is bound globally to "Win+Z" - pressing it instantly kills the app and terminates llama-server!
4. In the "Allowed Apps" tab:
   - Define custom app mappings (e.g., alias "notepad" -> "notepad.exe") that the AI is permitted to run.
5. In the "Voice Chat" tab:
   - Change the system prompt or personality on the fly.
   - Hold the on-screen "Push & Hold to Talk" button or press global "Win+Space" to speak your query.
   - Gemma-4 will respond spokenly and automatically execute specified mouse, keyboard, or system actions!
   - Anti-loop tracking is built-in (warns at 3x repetitions, stop alert at 4x, connection break at 5x).
