// Copyright © Go Opus Authors (see AUTHORS file)
//
// License for use of this code is detailed in the LICENSE file
//
// Modifications for WebAssembly by Jiang Yiheng

package opus

import (
	"context"
	"fmt"
	"runtime"

	"github.com/tetratelabs/wazero/api"
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
		if e.encoderPtr != 0 && e.wctx != nil && e.wctx.functions.Free != nil {
			// It's tricky to use context in finalizers.
			// Using context.Background() here, but be cautious.
			// We also need to ensure the module memory is still valid, which implies the runtime is alive.
			// The CloseWasmContext should be the primary mechanism for cleanup.
			// Finalizers are a fallback.
			// Directly call Free here as freeMemory helper returns an error we can't easily handle in a finalizer.
			_, finErr := e.wctx.functions.Free.Call(context.Background(), uint64(e.encoderPtr))
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

	opusEncoderGetSize := enc.wctx.functions.OpusEncoderGetSize
	if opusEncoderGetSize == nil {
		return fmt.Errorf("opus_encoder_get_size not found in Wasm functions cache")
	}

	results, err := opusEncoderGetSize.Call(ctx, uint64(channels))
	if err != nil {
		return fmt.Errorf("opus_encoder_get_size call failed: %w", err)
	}
	size := uint32(results[0])

	// Use wctx's malloc
	if enc.wctx.functions.Malloc == nil {
		return fmt.Errorf("wasm malloc function not initialized in encoder")
	}
	results, err = enc.wctx.functions.Malloc.Call(ctx, uint64(size))
	if err != nil {
		return fmt.Errorf("wasm malloc for encoder failed: %w", err)
	}
	enc.encoderPtr = uint32(results[0])
	if enc.encoderPtr == 0 {
		return fmt.Errorf("wasm malloc returned NULL for encoder")
	}

	opusEncoderInit := enc.wctx.functions.OpusEncoderInit
	if opusEncoderInit == nil {
		enc.wctx.freeMemory(ctx, enc.encoderPtr) // Clean up allocated memory
		enc.encoderPtr = 0
		return fmt.Errorf("opus_encoder_init not found in Wasm functions cache")
	}

	results, err = opusEncoderInit.Call(ctx, uint64(enc.encoderPtr), uint64(int32(sampleRate)), uint64(int32(channels)), uint64(int32(application)))
	if err != nil {
		enc.wctx.freeMemory(ctx, enc.encoderPtr) // Clean up
		enc.encoderPtr = 0
		return fmt.Errorf("opus_encoder_init call failed: %w", err)
	}
	errno := int32(results[0])
	if errno != opusOk { // opusOk is a global constant from wasm_context.go
		enc.wctx.freeMemory(ctx, enc.encoderPtr) // Clean up
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
	defer enc.wctx.freeMemory(ctx, pcmPtr)

	// For output, we need to allocate memory. The 'data' slice is the Go buffer.
	// We need to allocate Wasm memory of the same size for Opus to write into.
	dataWasmPtr, err := enc.wctx.writeToMemory(ctx, make([]byte, len(data))) // Allocate and get ptr
	if err != nil {
		return 0, fmt.Errorf("failed to allocate Wasm memory for output data: %w", err)
	}
	defer enc.wctx.freeMemory(ctx, dataWasmPtr)

	opusEncode := enc.wctx.functions.OpusEncode
	if opusEncode == nil {
		return 0, fmt.Errorf("opus_encode not found in Wasm functions cache")
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
	defer enc.wctx.freeMemory(ctx, pcmPtr)

	dataWasmPtr, err := enc.wctx.writeToMemory(ctx, make([]byte, len(data))) // Allocate for output
	if err != nil {
		return 0, fmt.Errorf("failed to allocate Wasm memory for output data: %w", err)
	}
	defer enc.wctx.freeMemory(ctx, dataWasmPtr)

	opusEncodeFloat := enc.wctx.functions.OpusEncodeFloat
	if opusEncodeFloat == nil {
		return 0, fmt.Errorf("opus_encode_float not found in Wasm functions cache")
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

func (enc *Encoder) setCtlInt32(ctlFunc api.Function, value int32) error {
	if enc.encoderPtr == 0 || enc.wctx == nil {
		return errEncUninitialized
	}
	if ctlFunc == nil {
		return fmt.Errorf("ctl function is nil for setCtlInt32")
	}
	ctx := context.Background()
	results, err := ctlFunc.Call(ctx, uint64(enc.encoderPtr), uint64(value))
	if err != nil {
		return fmt.Errorf("wasm ctl function call failed for setCtlInt32: %w", err)
	}
	res := int32(results[0])
	if res != opusOk {
		return Error(int(res))
	}
	return nil
}

func (enc *Encoder) getCtlInt32(ctlFunc api.Function) (int32, error) {
	if enc.encoderPtr == 0 || enc.wctx == nil {
		return 0, errEncUninitialized
	}
	if ctlFunc == nil {
		return 0, fmt.Errorf("ctl function is nil for getCtlInt32")
	}

	ctx := context.Background()
	valPtr, err := enc.wctx.allocateInt32Ptr(ctx) // Use method from wasmContext
	if err != nil {
		return 0, err
	}
	defer enc.wctx.freeMemory(ctx, valPtr) // Use free from wasmContext

	results, err := ctlFunc.Call(ctx, uint64(enc.encoderPtr), uint64(valPtr))
	if err != nil {
		return 0, fmt.Errorf("wasm ctl function call failed for getCtlInt32: %w", err)
	}
	res := int32(results[0])
	if res != opusOk { // opusOk is global
		return 0, Error(int(res)) // Error is global
	}
	value, ok := enc.wctx.module.Memory().ReadUint32Le(valPtr)
	if !ok {
		return 0, fmt.Errorf("failed to read value from Wasm memory for getCtlInt32 call")
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
	return enc.setCtlInt32(enc.wctx.functions.BridgeEncoderSetDtx, val)
}

// DTX reports whether this encoder is configured to use discontinuous transmission (DTX).
func (enc *Encoder) DTX() (bool, error) {
	val, err := enc.getCtlInt32(enc.wctx.functions.BridgeEncoderGetDtx)
	if err != nil {
		return false, err
	}
	return val != 0, nil
}

// InDTX returns whether the last encoded frame was either a comfort noise update or not encoded due to DTX.
func (enc *Encoder) InDTX() (bool, error) {
	val, err := enc.getCtlInt32(enc.wctx.functions.BridgeEncoderGetInDtx)
	if err != nil {
		return false, err
	}
	return val != 0, nil
}

// SampleRate returns the encoder sample rate in Hz.
func (enc *Encoder) SampleRate() (int, error) {
	val, err := enc.getCtlInt32(enc.wctx.functions.BridgeEncoderGetSampleRate)
	return int(val), err
}

// SetBitrate sets the bitrate of the Encoder.
func (enc *Encoder) SetBitrate(bitrate int) error {
	return enc.setCtlInt32(enc.wctx.functions.BridgeEncoderSetBitrate, int32(bitrate))
}

// SetBitrateToAuto allows the encoder to automatically set the bitrate.
func (enc *Encoder) SetBitrateToAuto() error {
	return enc.setCtlInt32(enc.wctx.functions.BridgeEncoderSetBitrate, opusAuto)
}

// SetBitrateToMax causes the encoder to use as much rate as it can.
func (enc *Encoder) SetBitrateToMax() error {
	return enc.setCtlInt32(enc.wctx.functions.BridgeEncoderSetBitrate, opusBitrateMax)
}

// Bitrate returns the bitrate of the Encoder.
func (enc *Encoder) Bitrate() (int, error) {
	val, err := enc.getCtlInt32(enc.wctx.functions.BridgeEncoderGetBitrate)
	return int(val), err
}

// SetComplexity sets the encoder's computational complexity.
func (enc *Encoder) SetComplexity(complexity int) error {
	return enc.setCtlInt32(enc.wctx.functions.BridgeEncoderSetComplexity, int32(complexity))
}

// Complexity returns the computational complexity used by the encoder.
func (enc *Encoder) Complexity() (int, error) {
	val, err := enc.getCtlInt32(enc.wctx.functions.BridgeEncoderGetComplexity)
	return int(val), err
}

// SetMaxBandwidth configures the maximum bandpass that the encoder will select automatically.
func (enc *Encoder) SetMaxBandwidth(maxBw Bandwidth) error {
	return enc.setCtlInt32(enc.wctx.functions.BridgeEncoderSetMaxBandwidth, int32(maxBw))
}

// MaxBandwidth gets the encoder's configured maximum allowed bandpass.
func (enc *Encoder) MaxBandwidth() (Bandwidth, error) {
	val, err := enc.getCtlInt32(enc.wctx.functions.BridgeEncoderGetMaxBandwidth)
	return Bandwidth(val), err
}

// SetInBandFEC configures the encoder's use of inband forward error correction (FEC).
func (enc *Encoder) SetInBandFEC(fec bool) error {
	val := int32(0)
	if fec {
		val = 1
	}
	return enc.setCtlInt32(enc.wctx.functions.BridgeEncoderSetInbandFec, val)
}

// InBandFEC gets the encoder's configured inband forward error correction (FEC).
func (enc *Encoder) InBandFEC() (bool, error) {
	val, err := enc.getCtlInt32(enc.wctx.functions.BridgeEncoderGetInbandFec)
	if err != nil {
		return false, err
	}
	return val != 0, nil
}

// SetPacketLossPerc configures the encoder's expected packet loss percentage.
func (enc *Encoder) SetPacketLossPerc(lossPerc int) error {
	return enc.setCtlInt32(enc.wctx.functions.BridgeEncoderSetPacketLossPerc, int32(lossPerc))
}

// PacketLossPerc gets the encoder's configured packet loss percentage.
func (enc *Encoder) PacketLossPerc() (int, error) {
	val, err := enc.getCtlInt32(enc.wctx.functions.BridgeEncoderGetPacketLossPerc)
	return int(val), err
}

// SetVBR configures the encoder's use of variable bitrate (VBR).
func (enc *Encoder) SetVBR(vbr bool) error {
	val := int32(0)
	if vbr {
		val = 1
	}
	return enc.setCtlInt32(enc.wctx.functions.BridgeEncoderSetVbr, int32(val))
}

// VBR reports whether this encoder is configured to use variable bitrate (VBR).
func (enc *Encoder) VBR() (bool, error) {
	val, err := enc.getCtlInt32(enc.wctx.functions.BridgeEncoderGetVbr)
	if err != nil {
		return false, err
	}
	return val != 0, err
}

// SetVBRConstraint configures the encoder's use of constrained VBR.
func (enc *Encoder) SetVBRConstraint(constraint bool) error {
	val := int32(0)
	if constraint {
		val = 1
	}
	return enc.setCtlInt32(enc.wctx.functions.BridgeEncoderSetVbrConstraint, val)
}

// VBRConstraint reports whether this encoder is configured to use constrained VBR.
func (enc *Encoder) VBRConstraint() (bool, error) {
	val, err := enc.getCtlInt32(enc.wctx.functions.BridgeEncoderGetVbrConstraint)
	if err != nil {
		return false, err
	}
	return val != 0, nil
}

// Reset resets the codec state to be equivalent to a freshly initialized state.
func (enc *Encoder) Reset() error {
	if enc.encoderPtr == 0 || enc.wctx == nil {
		return errEncUninitialized
	}
	resetFunc := enc.wctx.functions.BridgeEncoderResetState
	if resetFunc == nil {
		return fmt.Errorf("bridge_encoder_reset_state not found in Wasm functions cache")
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
