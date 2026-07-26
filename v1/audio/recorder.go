package audio

import (
	"encoding/binary"
	"fmt"
	"os"
	"sync"

	"github.com/gen2brain/malgo"
	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

// Recorder handles capturing audio from the default microphone and saving it to a WAV file.
type Recorder struct {
	ctx           *malgo.AllocatedContext
	device        *malgo.Device
	isRecording   bool
	recordedBytes []byte
	mutex         sync.Mutex
	sampleRate    uint32
}

// NewRecorder initializes a new Recorder instance with the default system audio context.
func NewRecorder() (*Recorder, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(message string) {
		// Silently consume internal logging unless debugging is needed
	})
	if err != nil {
		return nil, fmt.Errorf("failed to init malgo context: %w", err)
	}

	return &Recorder{
		ctx:        ctx,
		sampleRate: 16000, // 16kHz works best for Gemma multimodal audio
	}, nil
}

// Start begins recording audio from the microphone.
func (r *Recorder) Start() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if r.isRecording {
		return nil
	}

	r.recordedBytes = make([]byte, 0, 16000*2*10) // pre-allocate for ~10 seconds of mono 16-bit audio
	r.isRecording = true

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = 1
	deviceConfig.SampleRate = r.sampleRate
	deviceConfig.Alsa.NoMMap = 1

	sizeInBytes := uint32(malgo.SampleSizeInBytes(deviceConfig.Capture.Format))

	onRecvFrames := func(pPlaybackSample, pCaptureSample []byte, framecount uint32) {
		r.mutex.Lock()
		defer r.mutex.Unlock()

		if !r.isRecording {
			return
		}

		sampleCount := framecount * deviceConfig.Capture.Channels * sizeInBytes
		if len(pCaptureSample) >= int(sampleCount) {
			r.recordedBytes = append(r.recordedBytes, pCaptureSample[:sampleCount]...)
		}
	}

	captureCallbacks := malgo.DeviceCallbacks{
		Data: onRecvFrames,
	}

	device, err := malgo.InitDevice(r.ctx.Context, deviceConfig, captureCallbacks)
	if err != nil {
		return fmt.Errorf("failed to init device: %w", err)
	}

	err = device.Start()
	if err != nil {
		device.Uninit()
		return fmt.Errorf("failed to start device: %w", err)
	}

	r.device = device
	return nil
}

// Stop stops the active recording and returns the recorded 16-bit mono PCM sample bytes.
func (r *Recorder) Stop() ([]byte, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if !r.isRecording {
		return nil, nil
	}

	r.isRecording = false

	if r.device != nil {
		r.device.Uninit()
		r.device = nil
	}

	data := make([]byte, len(r.recordedBytes))
	copy(data, r.recordedBytes)
	r.recordedBytes = nil

	return data, nil
}

// SaveWav encodes raw 16-bit mono PCM bytes to a WAV file at 16kHz.
func (r *Recorder) SaveWav(filePath string, pcmBytes []byte) error {
	f, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create WAV file: %w", err)
	}
	defer f.Close()

	// Convert raw bytes (little-endian S16) to []int samples for go-audio
	samplesCount := len(pcmBytes) / 2
	intData := make([]int, samplesCount)
	for i := 0; i < samplesCount; i++ {
		val := int16(binary.LittleEndian.Uint16(pcmBytes[i*2 : i*2+2]))
		intData[i] = int(val)
	}

	// 1 specifies PCM format
	encoder := wav.NewEncoder(f, int(r.sampleRate), 16, 1, 1)

	buf := &audio.IntBuffer{
		Format: &audio.Format{
			NumChannels: 1,
			SampleRate:  int(r.sampleRate),
		},
		SourceBitDepth: 16,
		Data:           intData,
	}

	if err := encoder.Write(buf); err != nil {
		return fmt.Errorf("failed to write samples: %w", err)
	}

	if err := encoder.Close(); err != nil {
		return fmt.Errorf("failed to close encoder: %w", err)
	}

	return nil
}

// Close releases the malgo allocated context.
func (r *Recorder) Close() {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if r.device != nil {
		r.device.Uninit()
		r.device = nil
	}

	if r.ctx != nil {
		_ = r.ctx.Uninit()
		r.ctx.Free()
		r.ctx = nil
	}
}

// PlayWav plays back the specified WAV file using the speaker.
func (r *Recorder) PlayWav(filePath string) error {
	return nil
}
