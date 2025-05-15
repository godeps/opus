// Copyright © Go Opus Authors (see AUTHORS file)
//
// License for use of this code is detailed in the LICENSE file
//
// Modifications for WebAssembly by [Your Name/Organization]

package opus

import (
	"context"
	"fmt"
	"runtime"
	// "sync" // No longer needed here for initOnce
	// "github.com/tetratelabs/wazero" // Runtime managed by wasmContext
	// Still needed for api.Module etc. if used directly (but mostly via wasmContext)
)

var errEncUninitialized = fmt.Errorf("opus encoder uninitialized")

// Encoder contains the state of an Opus encoder using WebAssembly.
type Encoder struct {
	wctx       *wasmContext // Shared Wasm context
	encoderPtr uint32       // Pointer to the OpusEncoder struct in Wasm memory
	channels   int
}

// NewEncoder allocates a new Opus encoder and initializes it.
// wasmBinary is the []byte content of the opus.wasm file.
func NewEncoder(sampleRate int, channels int, application Application) (*Encoder, error) {
	ctx := context.Background() // Context for initialization
	wctx, err := GetWasmContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get wasm context for encoder: %w", err)
	}

	// malloc and free are now part of wctx, no need to export them separately here for the struct
	// if wasmModule is needed directly, it's wctx.module

	enc := &Encoder{
		wctx:     wctx,
		channels: channels,
		// module, malloc, free are now accessed via wctx
	}

	err = enc.init(ctx, sampleRate, channels, application)
	if err != nil {
		return nil, err
	}
	// Set finalizer to free Wasm memory when Encoder is GC'd
	runtime.SetFinalizer(enc, func(e *Encoder) {
		if e.encoderPtr != 0 && e.wctx != nil && e.wctx.free != nil {
			// It's tricky to use context in finalizers.
			// Using context.Background() here, but be cautious.
			// We also need to ensure the module memory is still valid, which implies the runtime is alive.
			// The CloseWasmContext should be the primary mechanism for cleanup.
			// Finalizers are a fallback.
			_, finErr := e.wctx.free.Call(context.Background(), uint64(e.encoderPtr))
			if finErr != nil {
				// Log error, as we can't return it from a finalizer
				fmt.Printf("opus: error freeing Wasm encoder memory in finalizer: %v\n", finErr)
			}
			e.encoderPtr = 0 // Mark as freed
		}
	})
	return enc, nil
}

func (enc *Encoder) init(ctx context.Context, sampleRate int, channels int, application Application) error {
	if enc.encoderPtr != 0 {
		return fmt.Errorf("opus encoder already initialized")
	}
	if channels != 1 && channels != 2 {
		return fmt.Errorf("number of channels must be 1 or 2: %d", channels)
	}

	if enc.wctx == nil || enc.wctx.module == nil {
		return fmt.Errorf("wasm context or module not initialized in encoder")
	}

	opusEncoderGetSize := enc.wctx.module.ExportedFunction("opus_encoder_get_size")
	if opusEncoderGetSize == nil {
		return fmt.Errorf("opus_encoder_get_size not found in Wasm module")
	}

	results, err := opusEncoderGetSize.Call(ctx, uint64(channels))
	if err != nil {
		return fmt.Errorf("opus_encoder_get_size call failed: %w", err)
	}
	size := uint32(results[0])

	// Use wctx's malloc
	results, err = enc.wctx.malloc.Call(ctx, uint64(size))
	if err != nil {
		return fmt.Errorf("wasm malloc for encoder failed: %w", err)
	}
	enc.encoderPtr = uint32(results[0])
	if enc.encoderPtr == 0 {
		return fmt.Errorf("wasm malloc returned NULL for encoder")
	}

	opusEncoderInit := enc.wctx.module.ExportedFunction("opus_encoder_init")
	if opusEncoderInit == nil {
		enc.wctx.free.Call(ctx, uint64(enc.encoderPtr)) // Clean up allocated memory
		enc.encoderPtr = 0
		return fmt.Errorf("opus_encoder_init not found in Wasm module")
	}

	results, err = opusEncoderInit.Call(ctx, uint64(enc.encoderPtr), uint64(int32(sampleRate)), uint64(int32(channels)), uint64(int32(application)))
	if err != nil {
		enc.wctx.free.Call(ctx, uint64(enc.encoderPtr)) // Clean up
		enc.encoderPtr = 0
		return fmt.Errorf("opus_encoder_init call failed: %w", err)
	}
	errno := int32(results[0])
	if errno != opusOk { // opusOk is a global constant from wasm_context.go
		enc.wctx.free.Call(ctx, uint64(enc.encoderPtr)) // Clean up
		enc.encoderPtr = 0
		return Error(int(errno))
	}
	return nil
}

// Encode raw PCM data (int16) and store the result in the supplied buffer.
func (enc *Encoder) Encode(pcm []int16, data []byte) (int, error) {
	if enc.encoderPtr == 0 {
		return 0, errEncUninitialized
	}
	if len(pcm) == 0 {
		return 0, fmt.Errorf("opus: no PCM data supplied")
	}
	if len(data) == 0 {
		return 0, fmt.Errorf("opus: no target buffer for encoded data")
	}
	if len(pcm)%enc.channels != 0 {
		return 0, fmt.Errorf("opus: input buffer length must be multiple of channels")
	}

	ctx := context.Background()
	samplesPerChannel := len(pcm) / enc.channels
	if enc.wctx == nil {
		return 0, errEncUninitialized // Or a more specific error
	}
	pcmBytes := int16SliceToByteSlice(pcm) // This helper is in wasm_context.go
	pcmPtr, err := enc.wctx.writeToMemory(ctx, pcmBytes)
	if err != nil {
		return 0, fmt.Errorf("failed to write PCM to Wasm memory: %w", err)
	}
	defer enc.wctx.free.Call(ctx, uint64(pcmPtr))

	// For output, we need to allocate memory. The 'data' slice is the Go buffer.
	// We need to allocate Wasm memory of the same size for Opus to write into.
	dataWasmPtr, err := enc.wctx.writeToMemory(ctx, make([]byte, len(data))) // Allocate and get ptr
	if err != nil {
		return 0, fmt.Errorf("failed to allocate Wasm memory for output data: %w", err)
	}
	defer enc.wctx.free.Call(ctx, uint64(dataWasmPtr))

	opusEncode := enc.wctx.module.ExportedFunction("opus_encode")
	if opusEncode == nil {
		return 0, fmt.Errorf("opus_encode not found in Wasm module")
	}

	results, err := opusEncode.Call(ctx,
		uint64(enc.encoderPtr),
		uint64(pcmPtr),                   // Source PCM in Wasm
		uint64(int32(samplesPerChannel)), // Frame size
		uint64(dataWasmPtr),              // Destination for encoded data in Wasm
		uint64(int32(len(data))),         // max_data_bytes (size of Go buffer 'data')
	)
	if err != nil {
		return 0, fmt.Errorf("opus_encode call failed: %w", err)
	}

	encodedBytes := int32(results[0])
	if encodedBytes < 0 {
		return 0, Error(int(encodedBytes)) // Error is a type in wasm_context.go or defined locally
	}

	// Read encoded data back from Wasm memory (dataWasmPtr) into the Go slice 'data'
	if uint32(encodedBytes) > uint32(len(data)) {
		return 0, fmt.Errorf("opus_encode reported %d bytes, but buffer has %d", encodedBytes, len(data))
	}
	encodedResult, ok := enc.wctx.module.Memory().Read(dataWasmPtr, uint32(encodedBytes))
	if !ok {
		return 0, fmt.Errorf("failed to read encoded data from Wasm memory: %d, %d", dataWasmPtr, encodedBytes)
	}
	copy(data, encodedResult)

	return int(encodedBytes), nil
}

// EncodeFloat32 raw PCM data (float32) and store the result.
func (enc *Encoder) EncodeFloat32(pcm []float32, data []byte) (int, error) {
	if enc.encoderPtr == 0 {
		return 0, errEncUninitialized
	}
	if len(pcm) == 0 {
		return 0, fmt.Errorf("opus: no PCM data supplied")
	}
	if len(data) == 0 {
		return 0, fmt.Errorf("opus: no target buffer for encoded data")
	}
	if len(pcm)%enc.channels != 0 {
		return 0, fmt.Errorf("opus: input buffer length must be multiple of channels")
	}

	ctx := context.Background()
	if enc.wctx == nil {
		return 0, errEncUninitialized
	}
	samplesPerChannel := len(pcm) / enc.channels
	pcmBytes := float32SliceToByteSlice(pcm) // This helper is in wasm_context.go
	pcmPtr, err := enc.wctx.writeToMemory(ctx, pcmBytes)
	if err != nil {
		return 0, fmt.Errorf("failed to write PCM to Wasm memory: %w", err)
	}
	defer enc.wctx.free.Call(ctx, uint64(pcmPtr))

	dataWasmPtr, err := enc.wctx.writeToMemory(ctx, make([]byte, len(data))) // Allocate for output
	if err != nil {
		return 0, fmt.Errorf("failed to allocate Wasm memory for output data: %w", err)
	}
	defer enc.wctx.free.Call(ctx, uint64(dataWasmPtr))

	opusEncodeFloat := enc.wctx.module.ExportedFunction("opus_encode_float")
	if opusEncodeFloat == nil {
		return 0, fmt.Errorf("opus_encode_float not found in Wasm module")
	}

	results, err := opusEncodeFloat.Call(ctx,
		uint64(enc.encoderPtr),
		uint64(pcmPtr),                   // Source PCM in Wasm
		uint64(int32(samplesPerChannel)), // Frame size
		uint64(dataWasmPtr),              // Destination for encoded data in Wasm
		uint64(int32(len(data))),         // max_data_bytes
	)
	if err != nil {
		return 0, fmt.Errorf("opus_encode_float call failed: %w", err)
	}

	encodedBytes := int32(results[0])
	if encodedBytes < 0 {
		return 0, Error(int(encodedBytes))
	}

	if uint32(encodedBytes) > uint32(len(data)) {
		return 0, fmt.Errorf("opus_encode_float reported %d bytes, but buffer has %d", encodedBytes, len(data))
	}
	encodedResult, ok := enc.wctx.module.Memory().Read(dataWasmPtr, uint32(encodedBytes))
	if !ok {
		return 0, fmt.Errorf("failed to read encoded data from Wasm memory")
	}
	copy(data, encodedResult)

	return int(encodedBytes), nil
}

// --- Generic CTL Getters/Setters ---

func (enc *Encoder) setCtlInt32(funcName string, value int32) error {
	if enc.encoderPtr == 0 || enc.wctx == nil {
		return errEncUninitialized
	}
	ctlFunc := enc.wctx.module.ExportedFunction(funcName)
	if ctlFunc == nil {
		return fmt.Errorf("%s not found in Wasm module (wctx module: %v)", funcName, enc.wctx.module)
	}
	ctx := context.Background()
	results, err := ctlFunc.Call(ctx, uint64(enc.encoderPtr), uint64(value))
	if err != nil {
		return fmt.Errorf("%s call failed: %w", funcName, err)
	}
	res := int32(results[0])
	if res != opusOk {
		return Error(int(res))
	}
	return nil
}

func (enc *Encoder) getCtlInt32(funcName string) (int32, error) {
	if enc.encoderPtr == 0 || enc.wctx == nil {
		return 0, errEncUninitialized
	}
	ctlFunc := enc.wctx.module.ExportedFunction(funcName)
	if ctlFunc == nil {
		return 0, fmt.Errorf("%s not found in Wasm module (wctx module: %v)", funcName, enc.wctx.module)
	}

	ctx := context.Background()
	valPtr, err := enc.wctx.allocateInt32Ptr(ctx) // Use method from wasmContext
	if err != nil {
		return 0, err
	}
	defer enc.wctx.free.Call(ctx, uint64(valPtr)) // Use free from wasmContext

	results, err := ctlFunc.Call(ctx, uint64(enc.encoderPtr), uint64(valPtr))
	if err != nil {
		return 0, fmt.Errorf("%s call failed: %w", funcName, err)
	}
	res := int32(results[0])
	if res != opusOk { // opusOk is global
		return 0, Error(int(res)) // Error is global
	}
	value, ok := enc.wctx.module.Memory().ReadUint32Le(valPtr)
	if !ok {
		return 0, fmt.Errorf("failed to read value from Wasm memory for %s", funcName)
	}
	return int32(value), nil
}

// --- Specific CTL Functions ---

// SetDTX configures the encoder's use of discontinuous transmission (DTX).
func (enc *Encoder) SetDTX(dtx bool) error {
	val := int32(0)
	if dtx {
		val = 1
	}
	return enc.setCtlInt32("bridge_encoder_set_dtx", val)
}

// DTX reports whether this encoder is configured to use discontinuous transmission (DTX).
func (enc *Encoder) DTX() (bool, error) {
	val, err := enc.getCtlInt32("bridge_encoder_get_dtx")
	if err != nil {
		return false, err
	}
	return val != 0, nil
}

// InDTX returns whether the last encoded frame was either a comfort noise update or not encoded due to DTX.
func (enc *Encoder) InDTX() (bool, error) {
	val, err := enc.getCtlInt32("bridge_encoder_get_in_dtx")
	if err != nil {
		return false, err
	}
	return val != 0, nil
}

// SampleRate returns the encoder sample rate in Hz.
func (enc *Encoder) SampleRate() (int, error) {
	val, err := enc.getCtlInt32("bridge_encoder_get_sample_rate")
	return int(val), err
}

// SetBitrate sets the bitrate of the Encoder.
func (enc *Encoder) SetBitrate(bitrate int) error {
	return enc.setCtlInt32("bridge_encoder_set_bitrate", int32(bitrate))
}

// SetBitrateToAuto allows the encoder to automatically set the bitrate.
func (enc *Encoder) SetBitrateToAuto() error {
	return enc.setCtlInt32("bridge_encoder_set_bitrate", opusAuto)
}

// SetBitrateToMax causes the encoder to use as much rate as it can.
func (enc *Encoder) SetBitrateToMax() error {
	return enc.setCtlInt32("bridge_encoder_set_bitrate", opusBitrateMax)
}

// Bitrate returns the bitrate of the Encoder.
func (enc *Encoder) Bitrate() (int, error) {
	val, err := enc.getCtlInt32("bridge_encoder_get_bitrate")
	return int(val), err
}

// SetComplexity sets the encoder's computational complexity.
func (enc *Encoder) SetComplexity(complexity int) error {
	return enc.setCtlInt32("bridge_encoder_set_complexity", int32(complexity))
}

// Complexity returns the computational complexity used by the encoder.
func (enc *Encoder) Complexity() (int, error) {
	val, err := enc.getCtlInt32("bridge_encoder_get_complexity")
	return int(val), err
}

// SetMaxBandwidth configures the maximum bandpass that the encoder will select automatically.
func (enc *Encoder) SetMaxBandwidth(maxBw Bandwidth) error {
	return enc.setCtlInt32("bridge_encoder_set_max_bandwidth", int32(maxBw))
}

// MaxBandwidth gets the encoder's configured maximum allowed bandpass.
func (enc *Encoder) MaxBandwidth() (Bandwidth, error) {
	val, err := enc.getCtlInt32("bridge_encoder_get_max_bandwidth")
	return Bandwidth(val), err
}

// SetInBandFEC configures the encoder's use of inband forward error correction (FEC).
func (enc *Encoder) SetInBandFEC(fec bool) error {
	val := int32(0)
	if fec {
		val = 1
	}
	return enc.setCtlInt32("bridge_encoder_set_inband_fec", val)
}

// InBandFEC gets the encoder's configured inband forward error correction (FEC).
func (enc *Encoder) InBandFEC() (bool, error) {
	val, err := enc.getCtlInt32("bridge_encoder_get_inband_fec")
	if err != nil {
		return false, err
	}
	return val != 0, nil
}

// SetPacketLossPerc configures the encoder's expected packet loss percentage.
func (enc *Encoder) SetPacketLossPerc(lossPerc int) error {
	return enc.setCtlInt32("bridge_encoder_set_packet_loss_perc", int32(lossPerc))
}

// PacketLossPerc gets the encoder's configured packet loss percentage.
func (enc *Encoder) PacketLossPerc() (int, error) {
	val, err := enc.getCtlInt32("bridge_encoder_get_packet_loss_perc")
	return int(val), err
}

// Reset resets the codec state to be equivalent to a freshly initialized state.
func (enc *Encoder) Reset() error {
	if enc.encoderPtr == 0 || enc.wctx == nil {
		return errEncUninitialized
	}
	resetFunc := enc.wctx.module.ExportedFunction("bridge_encoder_reset_state")
	if resetFunc == nil {
		return fmt.Errorf("bridge_encoder_reset_state not found in Wasm module (wctx module: %v)", enc.wctx.module)
	}
	ctx := context.Background()
	results, err := resetFunc.Call(ctx, uint64(enc.encoderPtr))
	if err != nil {
		return fmt.Errorf("bridge_encoder_reset_state call failed: %w", err)
	}
	res := int32(results[0])
	if res != opusOk {
		return Error(int(res))
	}
	return nil
}
