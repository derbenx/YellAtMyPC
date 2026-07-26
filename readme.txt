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
--> YellAtMyPC.exe

V1 is just chatting back and forth.
V2 will let you ask the AI to do things; open programs, click on things. type, write a program..

========================
How to Build and Compile
========================
To build the application, make sure you have Go installed on your machine.
Since Fyne requires graphics hardware integration, CGo must be enabled and you must have a C compiler installed.

On Windows:
  1. Install MSYS2 (https://www.msys2.org/).
  2. Install Mingw-w64 GCC by running the following in the MSYS2 terminal:
       pacman -S mingw-w64-x86_64-toolchain
  3. Ensure the bin directory of your Mingw-w64 installation (e.g. C:\msys64\mingw64\bin) is added to your Windows system PATH.
  4. Ensure CGO_ENABLED is enabled. In your Command Prompt or PowerShell, run:
       set CGO_ENABLED=1
       go build -o YellAtMyPC.exe

On Linux:
  1. Install development packages:
       sudo apt-get install -y libgl1-mesa-dev xorg-dev libegl1-mesa-dev libwayland-dev libxkbcommon-dev
  2. Run:
       CGO_ENABLED=1 go build

========================
How to Run & Configure
========================
1. Open YellAtMyPC.
2. In the "Setup / Config" tab:
   - Select either "Local Server (Self-Hosted)" or "Network / Remote Server".
   - Under "Local Llama discovery", click "Scan relative directories" to auto-populate files in "./llama" and "./ai".
   - Select your GGUF model, multimodal mproj file, and Llama server executable.
   - Set the port (default: 8080) and click "Launch Local Llama" to start llama-server in the background.
3. In the "Voice Chat" tab:
   - Select or edit the "AI Personality Prompt" directly.
   - Click and hold the large "Push & Hold to Talk" button to speak, then release to send.
   - Alternatively, use the global hotkey: hold "Win+Space" to record anywhere, and release to submit query!
   - Your audio query is sent to llama-server, processed, and the response is spoken out loud.
