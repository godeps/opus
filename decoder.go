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

	"github.com/tetratelabs/wazero/api" // Added for api.Function type
	// "unsafe" // Only needed if byte slice helpers using unsafe are copied here directly
	// "log" // Only if new logging specific to decoder is added
)

var errDecUninitialized = fmt.Errorf("opus decoder uninitialized")

// Decoder contains the state of an Opus decoder using WebAssembly.
type Decoder struct {
	wctx        *wasmContext // Shared Wasm context
	decoderPtr  uint32       // Pointer to the OpusDecoder struct in Wasm memory
	sample_rate int
	channels    int
	// module, malloc, free are now accessed via wctx
}

// NewDecoder allocates a new Opus decoder and initializes it.
// wasmBinary is the []byte content of the opus.wasm file.
func NewDecoder(sampleRate int, channels int) (*Decoder, error) {
	ctx := context.Background() // Context for initialization
	wctx, err := GetWasmContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get wasm context for decoder: %w", err)
	}

	// malloc and free are now part of wctx
	// if wctx.module == nil || wctx.malloc == nil || wctx.free == nil {
	// 	return nil, fmt.Errorf("Wasm context components (module, malloc, free) not properly initialized")
	// }

	dec := &Decoder{
		wctx:        wctx,
		sample_rate: sampleRate,
		channels:    channels,
	}

	err = dec.Init(sampleRate, channels)
	if err != nil {
		return nil, err
	}

	// Set finalizer to free Wasm memory when Decoder is GC'd
	runtime.SetFinalizer(dec, func(d *Decoder) {
		if d.decoderPtr != 0 && d.wctx != nil && d.wctx.functions.Free != nil {
			// Similar to Encoder, use context.Background() cautiously.
			// Directly call Free here as freeMemory helper returns an error we can't easily handle in a finalizer.
			_, finErr := d.wctx.functions.Free.Call(context.Background(), uint64(d.decoderPtr))
			if finErr != nil {
				fmt.Printf("opus: error freeing Wasm decoder memory in finalizer: %v\n", finErr)
			}
			d.decoderPtr = 0 // Mark as freed
		}
	})
	return dec, nil
}

// Init initializes a pre-allocated opus decoder.
func (dec *Decoder) Init(sampleRate int, channels int) error {
	if dec.decoderPtr != 0 {
		return fmt.Errorf("opus decoder already initialized")
	}
	if channels != 1 && channels != 2 {
		return fmt.Errorf("number of channels must be 1 or 2: %d", channels)
	}

	if dec.wctx == nil || dec.wctx.module == nil {
		return fmt.Errorf("wasm context or module not initialized in decoder")
	}
	ctx := context.Background()

	opusDecoderGetSize := dec.wctx.functions.OpusDecoderGetSize
	if opusDecoderGetSize == nil {
		return fmt.Errorf("opus_decoder_get_size not found in Wasm functions cache")
	}

	results, err := opusDecoderGetSize.Call(ctx, uint64(channels))
	if err != nil {
		return fmt.Errorf("opus_decoder_get_size call failed: %w", err)
	}
	size := uint32(results[0])

	if dec.wctx.functions.Malloc == nil {
		return fmt.Errorf("wasm malloc function not initialized in decoder")
	}
	results, err = dec.wctx.functions.Malloc.Call(ctx, uint64(size))
	if err != nil {
		return fmt.Errorf("wasm malloc for decoder failed: %w", err)
	}
	dec.decoderPtr = uint32(results[0])
	if dec.decoderPtr == 0 {
		return fmt.Errorf("wasm malloc returned NULL for decoder")
	}

	opusDecoderInit := dec.wctx.functions.OpusDecoderInit
	if opusDecoderInit == nil {
		dec.wctx.freeMemory(ctx, dec.decoderPtr) // Clean up
		dec.decoderPtr = 0
		return fmt.Errorf("opus_decoder_init not found in Wasm functions cache")
	}

	results, err = opusDecoderInit.Call(ctx, uint64(dec.decoderPtr), uint64(int32(sampleRate)), uint64(int32(channels)))
	if err != nil {
		dec.wctx.freeMemory(ctx, dec.decoderPtr) // Clean up
		dec.decoderPtr = 0
		return fmt.Errorf("opus_decoder_init call failed: %w", err)
	}
	errno := int32(results[0])
	if errno != opusOk { // opusOk is a global constant
		dec.wctx.freeMemory(ctx, dec.decoderPtr) // Clean up
		dec.decoderPtr = 0
		return Error(int(errno))
	}

	dec.sample_rate = sampleRate
	dec.channels = channels
	return nil
}

func (dec *Decoder) decodeInternal(data []byte, pcmPtr uint32, frameSize int, decodeFEC int, isFloat bool) (int, error) {
	if dec.decoderPtr == 0 || dec.wctx == nil {
		return 0, errDecUninitialized
	}

	ctx := context.Background()
	var dataPtr uint32
	var err error

	if len(data) > 0 {
		dataPtr, err = dec.wctx.writeToMemory(ctx, data) // Use method from wasmContext
		if err != nil {
			return 0, fmt.Errorf("failed to write input data to Wasm memory: %w", err)
		}
		defer dec.wctx.freeMemory(ctx, dataPtr) // Use free from wasmContext
	} else {
		// For PLC, data is NULL (represented by 0 pointer) and length is 0
		dataPtr = 0 // Remains 0 if data is nil or empty, writeToMemory handles malloc(0) if needed
	}

	dataLen := len(data)
	if data == nil { // for PLC
		dataLen = 0
	}

	var decodeFunc api.Function
	var funcNameForLog string // For logging purposes

	if isFloat {
		decodeFunc = dec.wctx.functions.OpusDecodeFloat
		funcNameForLog = "opus_decode_float"
	} else {
		decodeFunc = dec.wctx.functions.OpusDecode
		funcNameForLog = "opus_decode"
	}

	if decodeFunc == nil {
		return 0, fmt.Errorf("%s not found in Wasm functions cache", funcNameForLog)
	}

	results, err := decodeFunc.Call(ctx,
		uint64(dec.decoderPtr),
		uint64(dataPtr),          // pointer to encoded data, or 0 for PLC
		uint64(int32(dataLen)),   // length of data, or 0 for PLC
		uint64(pcmPtr),           // pointer to output PCM buffer
		uint64(int32(frameSize)), // frame size per channel
		uint64(int32(decodeFEC)), // 0 for no FEC, 1 for FEC
	)
	if err != nil {
		return 0, fmt.Errorf("%s call failed: %w", funcNameForLog, err)
	}

	samplesDecoded := int32(results[0])
	if samplesDecoded < 0 {
		return 0, Error(int(samplesDecoded))
	}
	return int(samplesDecoded), nil
}

// Decode encoded Opus data into the supplied int16 PCM buffer.
// Returns the number of decoded samples per channel.
func (dec *Decoder) Decode(data []byte, pcm []int16) (int, error) {
	if dec.wctx == nil {
		return 0, errDecUninitialized
	}
	if len(pcm) == 0 {
		return 0, fmt.Errorf("opus: target PCM buffer empty")
	}
	if cap(pcm)%dec.channels != 0 {
		return 0, fmt.Errorf("opus: target PCM buffer capacity must be multiple of channels")
	}

	ctx := context.Background()
	// pcmLenBytes := len(pcm) * 2 // 2 bytes per int16. This is for current length, cap is for max.
	// Max possible output size based on capacity
	pcmAllocSizeBytes := cap(pcm) * 2

	// We need to allocate memory for PCM output.
	// The current content of pcmDataForWasm (zeros) doesn't matter as Opus will overwrite it.
	// The size must be based on the capacity of the Go pcm slice to hold the decoded data.
	pcmDataForWasm := make([]byte, pcmAllocSizeBytes)          // Allocate based on capacity
	pcmPtr, err := dec.wctx.writeToMemory(ctx, pcmDataForWasm) // Effectively allocates
	if err != nil {
		return 0, fmt.Errorf("failed to allocate Wasm memory for PCM output: %w", err)
	}
	defer dec.wctx.freeMemory(ctx, pcmPtr)

	// frameSize is samples per channel, pcmLenBytes is total bytes for allocation
	frameSize := cap(pcm) / dec.channels
	samplesDecoded, err := dec.decodeInternal(data, pcmPtr, frameSize, 0, false)
	if err != nil {
		return 0, err
	}

	// Read decoded PCM data back from Wasm memory
	// int16SliceFromByteSlice is in wasm_context.go
	// Read up to the number of bytes corresponding to samplesDecoded
	bytesToRead := uint32(samplesDecoded * dec.channels * 2)
	if bytesToRead > uint32(pcmAllocSizeBytes) {
		return 0, fmt.Errorf("opus_decode returned more samples than buffer capacity: %d samples (%d bytes) vs %d bytes", samplesDecoded, bytesToRead, pcmAllocSizeBytes)
	}
	decodedBytes, ok := dec.wctx.module.Memory().Read(pcmPtr, bytesToRead)
	if !ok {
		return 0, fmt.Errorf("failed to read decoded PCM from Wasm memory")
	}
	if err := int16SliceFromByteSlice(decodedBytes, pcm[:samplesDecoded*dec.channels]); err != nil {
		return 0, fmt.Errorf("failed to convert bytes to int16 PCM: %w", err)
	}

	return samplesDecoded, nil
}

// DecodeFloat32 encoded Opus data into the supplied float32 PCM buffer.
// Returns the number of decoded samples per channel.
func (dec *Decoder) DecodeFloat32(data []byte, pcm []float32) (int, error) {
	if dec.wctx == nil {
		return 0, errDecUninitialized
	}
	if len(pcm) == 0 {
		return 0, fmt.Errorf("opus: target PCM buffer empty")
	}
	if cap(pcm)%dec.channels != 0 {
		return 0, fmt.Errorf("opus: target PCM buffer capacity must be multiple of channels")
	}

	ctx := context.Background()
	// pcmLenBytes := len(pcm) * 4 // 4 bytes per float32. For current length.
	pcmAllocSizeBytes := cap(pcm) * 4 // For capacity

	pcmDataForWasm := make([]byte, pcmAllocSizeBytes)          // Allocate based on capacity
	pcmPtr, err := dec.wctx.writeToMemory(ctx, pcmDataForWasm) // Effectively allocates
	if err != nil {
		return 0, fmt.Errorf("failed to allocate Wasm memory for PCM output: %w", err)
	}
	defer dec.wctx.freeMemory(ctx, pcmPtr)

	frameSize := cap(pcm) / dec.channels
	samplesDecoded, err := dec.decodeInternal(data, pcmPtr, frameSize, 0, true)
	if err != nil {
		return 0, err
	}

	bytesToRead := uint32(samplesDecoded * dec.channels * 4)
	if bytesToRead > uint32(pcmAllocSizeBytes) {
		return 0, fmt.Errorf("opus_decode_float returned more samples than buffer capacity: %d samples (%d bytes) vs %d bytes", samplesDecoded, bytesToRead, pcmAllocSizeBytes)
	}
	decodedBytes, ok := dec.wctx.module.Memory().Read(pcmPtr, bytesToRead)
	if !ok {
		return 0, fmt.Errorf("failed to read decoded PCM from Wasm memory")
	}
	// float32SliceFromByteSlice is in wasm_context.go
	if err := float32SliceFromByteSlice(decodedBytes, pcm[:samplesDecoded*dec.channels]); err != nil {
		return 0, fmt.Errorf("failed to convert bytes to float32 PCM: %w", err)
	}

	return samplesDecoded, nil
}

// DecodeFEC decodes a packet with FEC. pcm must be the size of the lost packet.
// Returns samples decoded per channel.
func (dec *Decoder) DecodeFEC(data []byte, pcm []int16) (int, error) {
	if dec.wctx == nil {
		return 0, errDecUninitialized
	}
	if len(pcm) == 0 {
		return 0, fmt.Errorf("opus: target PCM buffer empty for FEC")
	}
	if cap(pcm)%dec.channels != 0 {
		return 0, fmt.Errorf("opus: target PCM buffer capacity must be multiple of channels for FEC")
	}

	ctx := context.Background()
	pcmAllocSizeBytes := cap(pcm) * 2
	pcmDataForWasm := make([]byte, pcmAllocSizeBytes)
	pcmPtr, err := dec.wctx.writeToMemory(ctx, pcmDataForWasm)
	if err != nil {
		return 0, fmt.Errorf("failed to allocate Wasm memory for FEC PCM output: %w", err)
	}
	defer dec.wctx.freeMemory(ctx, pcmPtr)

	frameSize := cap(pcm) / dec.channels
	samplesDecoded, err := dec.decodeInternal(data, pcmPtr, frameSize, 1, false) // decode_fec = 1
	if err != nil {
		return 0, err
	}

	bytesToRead := uint32(samplesDecoded * dec.channels * 2)
	if bytesToRead > uint32(pcmAllocSizeBytes) {
		return 0, fmt.Errorf("opus_decode (FEC) returned more samples than buffer capacity: %d samples (%d bytes) vs %d bytes", samplesDecoded, bytesToRead, pcmAllocSizeBytes)
	}
	decodedBytes, ok := dec.wctx.module.Memory().Read(pcmPtr, bytesToRead)
	if !ok {
		return 0, fmt.Errorf("failed to read FEC decoded PCM from Wasm memory")
	}
	if err := int16SliceFromByteSlice(decodedBytes, pcm[:samplesDecoded*dec.channels]); err != nil {
		return 0, fmt.Errorf("failed to convert bytes to int16 FEC PCM: %w", err)
	}
	return samplesDecoded, nil
}

// DecodeFECFloat32 decodes a packet with FEC. pcm must be the size of the lost packet.
// Returns samples decoded per channel.
func (dec *Decoder) DecodeFECFloat32(data []byte, pcm []float32) (int, error) {
	if dec.wctx == nil {
		return 0, errDecUninitialized
	}
	if len(pcm) == 0 {
		return 0, fmt.Errorf("opus: target PCM buffer empty for FEC")
	}
	if cap(pcm)%dec.channels != 0 {
		return 0, fmt.Errorf("opus: target PCM buffer capacity must be multiple of channels for FEC")
	}

	ctx := context.Background()
	pcmAllocSizeBytes := cap(pcm) * 4
	pcmDataForWasm := make([]byte, pcmAllocSizeBytes)
	pcmPtr, err := dec.wctx.writeToMemory(ctx, pcmDataForWasm)
	if err != nil {
		return 0, fmt.Errorf("failed to allocate Wasm memory for FEC PCM output: %w", err)
	}
	defer dec.wctx.freeMemory(ctx, pcmPtr)

	frameSize := cap(pcm) / dec.channels
	samplesDecoded, err := dec.decodeInternal(data, pcmPtr, frameSize, 1, true) // decode_fec = 1
	if err != nil {
		return 0, err
	}

	bytesToRead := uint32(samplesDecoded * dec.channels * 4)
	if bytesToRead > uint32(pcmAllocSizeBytes) {
		return 0, fmt.Errorf("opus_decode_float (FEC) returned more samples than buffer capacity: %d samples (%d bytes) vs %d bytes", samplesDecoded, bytesToRead, pcmAllocSizeBytes)
	}
	decodedBytes, ok := dec.wctx.module.Memory().Read(pcmPtr, bytesToRead)
	if !ok {
		return 0, fmt.Errorf("failed to read FEC decoded PCM from Wasm memory")
	}
	if err := float32SliceFromByteSlice(decodedBytes, pcm[:samplesDecoded*dec.channels]); err != nil {
		return 0, fmt.Errorf("failed to convert bytes to float32 FEC PCM: %w", err)
	}
	return samplesDecoded, nil
}

// DecodePLC recovers a lost packet using PLC. pcm must be the size of the lost packet.
// Returns samples decoded per channel.
func (dec *Decoder) DecodePLC(pcm []int16) (int, error) {
	if dec.wctx == nil {
		return 0, errDecUninitialized
	}
	if len(pcm) == 0 {
		return 0, fmt.Errorf("opus: target PCM buffer empty for PLC")
	}
	if cap(pcm)%dec.channels != 0 {
		return 0, fmt.Errorf("opus: target PCM buffer capacity must be multiple of channels for PLC")
	}

	ctx := context.Background()
	pcmAllocSizeBytes := cap(pcm) * 2
	pcmDataForWasm := make([]byte, pcmAllocSizeBytes)
	pcmPtr, err := dec.wctx.writeToMemory(ctx, pcmDataForWasm)
	if err != nil {
		return 0, fmt.Errorf("failed to allocate Wasm memory for PLC PCM output: %w", err)
	}
	defer dec.wctx.freeMemory(ctx, pcmPtr)

	frameSize := cap(pcm) / dec.channels
	// For PLC, data is NULL (dataPtr=0) and dataLen is 0. decodeInternal handles data=nil.
	samplesDecoded, err := dec.decodeInternal(nil, pcmPtr, frameSize, 0, false)
	if err != nil {
		return 0, err
	}

	bytesToRead := uint32(samplesDecoded * dec.channels * 2)
	if bytesToRead > uint32(pcmAllocSizeBytes) {
		return 0, fmt.Errorf("opus_decode (PLC) returned more samples than buffer capacity: %d samples (%d bytes) vs %d bytes", samplesDecoded, bytesToRead, pcmAllocSizeBytes)
	}
	decodedBytes, ok := dec.wctx.module.Memory().Read(pcmPtr, bytesToRead)
	if !ok {
		return 0, fmt.Errorf("failed to read PLC decoded PCM from Wasm memory")
	}
	if err := int16SliceFromByteSlice(decodedBytes, pcm[:samplesDecoded*dec.channels]); err != nil {
		return 0, fmt.Errorf("failed to convert bytes to int16 PLC PCM: %w", err)
	}
	return samplesDecoded, nil
}

// DecodePLCFloat32 recovers a lost packet using PLC. pcm must be the size of the lost packet.
// Returns samples decoded per channel.
func (dec *Decoder) DecodePLCFloat32(pcm []float32) (int, error) {
	if dec.wctx == nil {
		return 0, errDecUninitialized
	}
	if len(pcm) == 0 {
		return 0, fmt.Errorf("opus: target PCM buffer empty for PLC")
	}
	if cap(pcm)%dec.channels != 0 {
		return 0, fmt.Errorf("opus: target PCM buffer capacity must be multiple of channels for PLC")
	}

	ctx := context.Background()
	pcmAllocSizeBytes := cap(pcm) * 4
	pcmDataForWasm := make([]byte, pcmAllocSizeBytes)
	pcmPtr, err := dec.wctx.writeToMemory(ctx, pcmDataForWasm)
	if err != nil {
		return 0, fmt.Errorf("failed to allocate Wasm memory for PLC PCM output: %w", err)
	}
	defer dec.wctx.freeMemory(ctx, pcmPtr)

	frameSize := cap(pcm) / dec.channels
	samplesDecoded, err := dec.decodeInternal(nil, pcmPtr, frameSize, 0, true)
	if err != nil {
		return 0, err
	}

	bytesToRead := uint32(samplesDecoded * dec.channels * 4)
	if bytesToRead > uint32(pcmAllocSizeBytes) {
		return 0, fmt.Errorf("opus_decode_float (PLC) returned more samples than buffer capacity: %d samples (%d bytes) vs %d bytes", samplesDecoded, bytesToRead, pcmAllocSizeBytes)
	}
	decodedBytes, ok := dec.wctx.module.Memory().Read(pcmPtr, bytesToRead)
	if !ok {
		return 0, fmt.Errorf("failed to read PLC decoded PCM from Wasm memory")
	}
	if err := float32SliceFromByteSlice(decodedBytes, pcm[:samplesDecoded*dec.channels]); err != nil {
		return 0, fmt.Errorf("failed to convert bytes to float32 PLC PCM: %w", err)
	}
	return samplesDecoded, nil
}

// LastPacketDuration gets the duration (in samples per channel) of the last successfully decoded/concealed packet.
func (dec *Decoder) LastPacketDuration() (int, error) {
	if dec.decoderPtr == 0 || dec.wctx == nil {
		return 0, errDecUninitialized
	}
	ctlFunc := dec.wctx.functions.BridgeDecoderGetLastPacketDuration
	if ctlFunc == nil {
		return 0, fmt.Errorf("bridge_decoder_get_last_packet_duration not found in Wasm functions cache")
	}

	ctx := context.Background()
	samplesPtr, err := dec.wctx.allocateInt32Ptr(ctx) // Use method from wasmContext
	if err != nil {
		return 0, err
	}
	defer dec.wctx.freeMemory(ctx, samplesPtr) // Use free from wasmContext

	results, err := ctlFunc.Call(ctx, uint64(dec.decoderPtr), uint64(samplesPtr))
	if err != nil {
		return 0, fmt.Errorf("bridge_decoder_get_last_packet_duration call failed: %w", err)
	}
	res := int32(results[0])
	if res != opusOk { // opusOk is global
		return 0, Error(int(res)) // Error is global
	}
	samplesValue, ok := dec.wctx.module.Memory().ReadUint32Le(samplesPtr)
	if !ok {
		return 0, fmt.Errorf("failed to read last packet duration from Wasm memory")
	}
	return int(samplesValue), nil
}
