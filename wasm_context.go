// Copyright © Go Opus Authors (see AUTHORS file)
//
// License for use of this code is detailed in the LICENSE file
//
// Shared WebAssembly context for libopus-c2go

package opus

import (
	"context"
	"fmt"
	"log"
	"sync"
	"unsafe"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	_ "embed"
)

//go:embed wasm-bridge/build/wasm_bridge
var opusWasmBinary []byte

// WasmFunctions holds cached an api.Function instances from the Wasm module.
type WasmFunctions struct {
	// Common
	Malloc api.Function
	Free   api.Function

	// Encoder functions
	OpusEncoderGetSize             api.Function
	OpusEncoderInit                api.Function
	OpusEncode                     api.Function
	OpusEncodeFloat                api.Function
	BridgeEncoderSetDtx            api.Function
	BridgeEncoderGetDtx            api.Function
	BridgeEncoderGetInDtx          api.Function
	BridgeEncoderGetSampleRate     api.Function
	BridgeEncoderSetBitrate        api.Function
	BridgeEncoderGetBitrate        api.Function
	BridgeEncoderSetComplexity     api.Function
	BridgeEncoderGetComplexity     api.Function
	BridgeEncoderSetMaxBandwidth   api.Function
	BridgeEncoderGetMaxBandwidth   api.Function
	BridgeEncoderSetInbandFec      api.Function
	BridgeEncoderGetInbandFec      api.Function
	BridgeEncoderSetPacketLossPerc api.Function
	BridgeEncoderGetPacketLossPerc api.Function
	BridgeEncoderSetVbr            api.Function
	BridgeEncoderGetVbr            api.Function
	BridgeEncoderSetVbrConstraint  api.Function
	BridgeEncoderGetVbrConstraint  api.Function
	BridgeEncoderResetState        api.Function

	// Decoder functions
	OpusDecoderGetSize                 api.Function
	OpusDecoderInit                    api.Function
	OpusDecode                         api.Function
	OpusDecodeFloat                    api.Function
	BridgeDecoderGetLastPacketDuration api.Function

	// Constant getter functions
	GetOpusOkAddress                     api.Function
	GetOpusBadArgAddress                 api.Function
	GetOpusBufferTooSmallAddress         api.Function
	GetOpusInternalErrorAddress          api.Function
	GetOpusInvalidPacketAddress          api.Function
	GetOpusUnimplementedAddress          api.Function
	GetOpusInvalidStateAddress           api.Function
	GetOpusAllocFailAddress              api.Function
	GetOpusBandwidthNarrowbandAddress    api.Function
	GetOpusBandwidthMediumbandAddress    api.Function
	GetOpusBandwidthWidebandAddress      api.Function
	GetOpusBandwidthSuperWidebandAddress api.Function
	GetOpusBandwidthFullbandAddress      api.Function
	GetOpusAutoAddress                   api.Function
	GetOpusBitrateMaxAddress             api.Function
}

// wasmContext holds the shared Wasm runtime, module, and cached functions.
type wasmContext struct {
	runtime   wazero.Runtime
	module    api.Module
	functions WasmFunctions // Changed from malloc/free fields + map to a struct
	// Add other shared components here if needed
}

var (
	globalWasmContext *wasmContext
	wasmInitOnce      sync.Once
	wasmInitErr       error
)

// Constants to be loaded from Wasm
var (
	opusOk                     int32
	opusBadArg                 int32
	opusBufferTooSmall         int32
	opusInternalError          int32
	opusInvalidPacket          int32
	opusUnimplemented          int32
	opusInvalidState           int32
	opusAllocFail              int32
	opusBandwidthNarrowband    int32
	opusBandwidthMediumband    int32
	opusBandwidthWideband      int32
	opusBandwidthSuperWideband int32
	opusBandwidthFullband      int32
	opusAuto                   int32
	opusBitrateMax             int32
)

type Bandwidth int32

var ( // Changed from const to var
	// Values will be initialized from Wasm
	Narrowband    Bandwidth
	Mediumband    Bandwidth
	Wideband      Bandwidth
	SuperWideband Bandwidth
	Fullband      Bandwidth
)

// initWasm initializes the Wazero runtime, compiles the wasm module, and loads constants.
// It is designed to be called multiple times but only executes the initialization logic once.
func initWasm(ctx context.Context, wasmBinary []byte) error {
	_ = ctx

	wasmInitOnce.Do(func() {
		initCtx := context.Background()
		rt := wazero.NewRuntime(initCtx)
		wasi_snapshot_preview1.MustInstantiate(initCtx, rt)

		compiledModule, err := rt.CompileModule(initCtx, wasmBinary)
		if err != nil {
			wasmInitErr = fmt.Errorf("failed to compile wasm module: %w", err)
			log.Printf("initWasm: %v", wasmInitErr)
			rt.Close(initCtx)
			return
		}

		cfg := wazero.NewModuleConfig().WithName("opus-global")
		mod, err := rt.InstantiateModule(initCtx, compiledModule, cfg)
		if err != nil {
			wasmInitErr = fmt.Errorf("failed to instantiate wasm module: %w", err)
			log.Printf("initWasm: %v", wasmInitErr)
			rt.Close(initCtx)
			compiledModule.Close(initCtx)
			return
		}

		var funcs WasmFunctions
		loadFunc := func(name string) api.Function {
			f := mod.ExportedFunction(name)
			if f == nil && wasmInitErr == nil { // Only set error if not already set
				wasmInitErr = fmt.Errorf("wasm function %s not found", name)
				log.Printf("initWasm: %v", wasmInitErr)
			}
			return f
		}

		// Common
		funcs.Malloc = loadFunc("malloc")
		funcs.Free = loadFunc("free")

		// Encoder functions
		funcs.OpusEncoderGetSize = loadFunc("opus_encoder_get_size")
		funcs.OpusEncoderInit = loadFunc("opus_encoder_init")
		funcs.OpusEncode = loadFunc("opus_encode")
		funcs.OpusEncodeFloat = loadFunc("opus_encode_float")
		funcs.BridgeEncoderSetDtx = loadFunc("bridge_encoder_set_dtx")
		funcs.BridgeEncoderGetDtx = loadFunc("bridge_encoder_get_dtx")
		funcs.BridgeEncoderGetInDtx = loadFunc("bridge_encoder_get_in_dtx")
		funcs.BridgeEncoderGetSampleRate = loadFunc("bridge_encoder_get_sample_rate")
		funcs.BridgeEncoderSetBitrate = loadFunc("bridge_encoder_set_bitrate")
		funcs.BridgeEncoderGetBitrate = loadFunc("bridge_encoder_get_bitrate")
		funcs.BridgeEncoderSetComplexity = loadFunc("bridge_encoder_set_complexity")
		funcs.BridgeEncoderGetComplexity = loadFunc("bridge_encoder_get_complexity")
		funcs.BridgeEncoderSetMaxBandwidth = loadFunc("bridge_encoder_set_max_bandwidth")
		funcs.BridgeEncoderGetMaxBandwidth = loadFunc("bridge_encoder_get_max_bandwidth")
		funcs.BridgeEncoderSetInbandFec = loadFunc("bridge_encoder_set_inband_fec")
		funcs.BridgeEncoderGetInbandFec = loadFunc("bridge_encoder_get_inband_fec")
		funcs.BridgeEncoderSetPacketLossPerc = loadFunc("bridge_encoder_set_packet_loss_perc")
		funcs.BridgeEncoderGetPacketLossPerc = loadFunc("bridge_encoder_get_packet_loss_perc")
		funcs.BridgeEncoderSetVbr = loadFunc("bridge_encoder_set_vbr")
		funcs.BridgeEncoderGetVbr = loadFunc("bridge_encoder_get_vbr")
		funcs.BridgeEncoderSetVbrConstraint = loadFunc("bridge_encoder_set_vbr_constraint")
		funcs.BridgeEncoderGetVbrConstraint = loadFunc("bridge_encoder_get_vbr_constraint")
		funcs.BridgeEncoderResetState = loadFunc("bridge_encoder_reset_state")

		// Decoder functions
		funcs.OpusDecoderGetSize = loadFunc("opus_decoder_get_size")
		funcs.OpusDecoderInit = loadFunc("opus_decoder_init")
		funcs.OpusDecode = loadFunc("opus_decode")
		funcs.OpusDecodeFloat = loadFunc("opus_decode_float")
		funcs.BridgeDecoderGetLastPacketDuration = loadFunc("bridge_decoder_get_last_packet_duration")

		// Constant getter functions
		funcs.GetOpusOkAddress = loadFunc("get_opus_ok_address")
		funcs.GetOpusBadArgAddress = loadFunc("get_opus_bad_arg_address")
		funcs.GetOpusBufferTooSmallAddress = loadFunc("get_opus_buffer_too_small_address")
		funcs.GetOpusInternalErrorAddress = loadFunc("get_opus_internal_error_address")
		funcs.GetOpusInvalidPacketAddress = loadFunc("get_opus_invalid_packet_address")
		funcs.GetOpusUnimplementedAddress = loadFunc("get_opus_unimplemented_address")
		funcs.GetOpusInvalidStateAddress = loadFunc("get_opus_invalid_state_address")
		funcs.GetOpusAllocFailAddress = loadFunc("get_opus_alloc_fail_address")
		funcs.GetOpusBandwidthNarrowbandAddress = loadFunc("get_opus_bandwidth_narrowband_address")
		funcs.GetOpusBandwidthMediumbandAddress = loadFunc("get_opus_bandwidth_mediumband_address")
		funcs.GetOpusBandwidthWidebandAddress = loadFunc("get_opus_bandwidth_wideband_address")
		funcs.GetOpusBandwidthSuperWidebandAddress = loadFunc("get_opus_bandwidth_superwideband_address")
		funcs.GetOpusBandwidthFullbandAddress = loadFunc("get_opus_bandwidth_fullband_address")
		funcs.GetOpusAutoAddress = loadFunc("get_opus_auto_address")
		funcs.GetOpusBitrateMaxAddress = loadFunc("get_opus_bitrate_max_address")

		if wasmInitErr != nil {
			// If any function failed to load, wasmInitErr is set. Clean up.
			rt.Close(initCtx)
			compiledModule.Close(initCtx)
			mod.Close(initCtx) // mod might be nil if instantiation failed earlier, but Close handles nil.
			return
		}

		globalWasmContext = &wasmContext{
			runtime:   rt,
			module:    mod,
			functions: funcs,
		}

		if err := loadOpusConstants(initCtx, globalWasmContext); err != nil {
			wasmInitErr = fmt.Errorf("failed to load opus constants from wasm: %w", err)
			log.Printf("initWasm: %v", wasmInitErr)
			// Cleanup will be handled by CloseWasmContext or finalizers if this part fails
			return
		}
	})

	return wasmInitErr
}

// mustReadInt32Constant reads an int32 constant from wasm memory via an exported getter function.
// It now takes the api.Function directly.
func mustReadInt32Constant(ctx context.Context, module api.Module, fn api.Function, funcNameForLog string) int32 {
	if fn == nil { // Should have been caught during initWasm
		log.Fatalf("Wasm function for %s is nil", funcNameForLog)
	}
	results, err := fn.Call(ctx)
	if err != nil {
		log.Fatalf("Failed to call %s: %v", funcNameForLog, err)
	}
	ptr := uint32(results[0])
	val, ok := module.Memory().ReadUint32Le(ptr)
	if !ok {
		log.Fatalf("Failed to read memory at %d for %s", ptr, funcNameForLog)
	}
	return int32(val)
}

// loadOpusConstants loads the Opus constants from the wasm module into global variables.
func loadOpusConstants(ctx context.Context, wc *wasmContext) error {
	// Pass the module and the specific function from the cached WasmFunctions struct
	opusOk = mustReadInt32Constant(ctx, wc.module, wc.functions.GetOpusOkAddress, "get_opus_ok_address")
	opusBadArg = mustReadInt32Constant(ctx, wc.module, wc.functions.GetOpusBadArgAddress, "get_opus_bad_arg_address")
	opusBufferTooSmall = mustReadInt32Constant(ctx, wc.module, wc.functions.GetOpusBufferTooSmallAddress, "get_opus_buffer_too_small_address")
	opusInternalError = mustReadInt32Constant(ctx, wc.module, wc.functions.GetOpusInternalErrorAddress, "get_opus_internal_error_address")
	opusInvalidPacket = mustReadInt32Constant(ctx, wc.module, wc.functions.GetOpusInvalidPacketAddress, "get_opus_invalid_packet_address")
	opusUnimplemented = mustReadInt32Constant(ctx, wc.module, wc.functions.GetOpusUnimplementedAddress, "get_opus_unimplemented_address")
	opusInvalidState = mustReadInt32Constant(ctx, wc.module, wc.functions.GetOpusInvalidStateAddress, "get_opus_invalid_state_address")
	opusAllocFail = mustReadInt32Constant(ctx, wc.module, wc.functions.GetOpusAllocFailAddress, "get_opus_alloc_fail_address")

	opusBandwidthNarrowband = mustReadInt32Constant(ctx, wc.module, wc.functions.GetOpusBandwidthNarrowbandAddress, "get_opus_bandwidth_narrowband_address")
	opusBandwidthMediumband = mustReadInt32Constant(ctx, wc.module, wc.functions.GetOpusBandwidthMediumbandAddress, "get_opus_bandwidth_mediumband_address")
	opusBandwidthWideband = mustReadInt32Constant(ctx, wc.module, wc.functions.GetOpusBandwidthWidebandAddress, "get_opus_bandwidth_wideband_address")
	opusBandwidthSuperWideband = mustReadInt32Constant(ctx, wc.module, wc.functions.GetOpusBandwidthSuperWidebandAddress, "get_opus_bandwidth_superwideband_address")
	opusBandwidthFullband = mustReadInt32Constant(ctx, wc.module, wc.functions.GetOpusBandwidthFullbandAddress, "get_opus_bandwidth_fullband_address")

	Narrowband = Bandwidth(opusBandwidthNarrowband)
	Mediumband = Bandwidth(opusBandwidthMediumband)
	Wideband = Bandwidth(opusBandwidthWideband)
	SuperWideband = Bandwidth(opusBandwidthSuperWideband)
	Fullband = Bandwidth(opusBandwidthFullband)

	opusAuto = mustReadInt32Constant(ctx, wc.module, wc.functions.GetOpusAutoAddress, "get_opus_auto_address")
	opusBitrateMax = mustReadInt32Constant(ctx, wc.module, wc.functions.GetOpusBitrateMaxAddress, "get_opus_bitrate_max_address")

	return nil
}

// GetWasmContext returns the initialized global Wasm context.
// It will trigger initialization if not already done.
func GetWasmContext(ctx context.Context) (*wasmContext, error) {
	if err := initWasm(ctx, opusWasmBinary); err != nil {
		return nil, fmt.Errorf("failed to initialize wasm context: %w", err)
	}
	return globalWasmContext, nil
}

// CloseWasmContext closes the global Wasm runtime.
// This should typically be called when the application exits.
func CloseWasmContext(ctx context.Context) error {
	if globalWasmContext != nil && globalWasmContext.runtime != nil {
		err := globalWasmContext.runtime.Close(ctx)
		globalWasmContext.runtime = nil // Prevent double close
		globalWasmContext.module = nil
		// globalWasmContext.malloc = nil // These are now part of globalWasmContext.functions
		// globalWasmContext.free = nil
		globalWasmContext.functions = WasmFunctions{} // Clear cached functions struct
		globalWasmContext = nil                       // Clear the global context
		wasmInitOnce = sync.Once{}                    // Reset the initOnce for potential re-init in tests etc.
		wasmInitErr = nil
		return err
	}
	return nil // Already closed or not initialized
}

// --- Shared Helper functions for wasm memory management ---
// These were in encoder.go and decoder.go and are now moved here to be shared.

// writeToMemory writes a Go byte slice to wasm memory using the wasmContext's malloc.
func (wc *wasmContext) writeToMemory(ctx context.Context, data []byte) (ptr uint32, err error) {
	byteCount := uint32(len(data))
	// Note: malloc(0) behavior can be platform-dependent. Wazero's malloc (if from emscripten)
	// might return a non-NULL pointer or NULL. If it's NULL, and byteCount is 0, it's fine.
	// If byteCount > 0 and malloc returns NULL, that's an error.

	if wc.functions.Malloc == nil {
		return 0, fmt.Errorf("wasm malloc function not initialized in wasmContext")
	}

	results, err := wc.functions.Malloc.Call(ctx, uint64(byteCount))
	if err != nil {
		return 0, fmt.Errorf("wasm malloc failed: %w", err)
	}
	ptr = uint32(results[0])
	if ptr == 0 && byteCount > 0 {
		return 0, fmt.Errorf("wasm malloc returned NULL for non-zero size (%d bytes)", byteCount)
	}

	if byteCount > 0 {
		if !wc.module.Memory().Write(ptr, data) {
			if ptr != 0 && wc.functions.Free != nil {
				// Attempt to free if write failed, but only if free is available
				wc.functions.Free.Call(ctx, uint64(ptr))
			}
			return 0, fmt.Errorf("wasm memory write failed")
		}
	}
	return ptr, nil
}

// allocateInt32Ptr allocates memory for an int32 in wasm memory using the wasmContext's malloc.
func (wc *wasmContext) allocateInt32Ptr(ctx context.Context) (ptr uint32, err error) {
	if wc.functions.Malloc == nil {
		return 0, fmt.Errorf("wasm malloc function not initialized in wasmContext for allocateInt32Ptr")
	}
	results, err := wc.functions.Malloc.Call(ctx, 4) // sizeof(int32) is 4
	if err != nil {
		return 0, fmt.Errorf("wasm malloc for int32 ptr failed: %w", err)
	}
	ptr = uint32(results[0])
	if ptr == 0 {
		return 0, fmt.Errorf("wasm malloc for int32 ptr returned NULL")
	}
	return ptr, nil
}

// freeMemory calls the Wasm free function.
func (wc *wasmContext) freeMemory(ctx context.Context, ptr uint32) error {
	if ptr == 0 {
		return nil // Freeing a null pointer is a no-op.
	}
	if wc.functions.Free == nil {
		return fmt.Errorf("wasm free function not initialized in wasmContext")
	}
	_, err := wc.functions.Free.Call(ctx, uint64(ptr))
	if err != nil {
		return fmt.Errorf("wasm free call failed: %w", err)
	}
	return nil
}

// --- Shared Helper functions for byte slice conversions ---
// These handle endianness correctly (Wasm is little-endian).

// int16SliceToByteSlice converts an int16 slice to a little-endian byte slice.
func int16SliceToByteSlice(s []int16) []byte {
	b := make([]byte, len(s)*2)
	for i, v := range s {
		b[i*2] = byte(v)
		b[i*2+1] = byte(v >> 8)
	}
	return b
}

// int16SliceFromByteSlice converts a little-endian byte slice to an int16 slice.
func int16SliceFromByteSlice(src []byte, dest []int16) error {
	if len(src)%2 != 0 {
		return fmt.Errorf("byte slice length %d is not a multiple of 2 for int16 conversion", len(src))
	}
	if len(dest)*2 < len(src) {
		return fmt.Errorf("destination int16 slice too small (len %d) for byte slice (len %d)", len(dest), len(src))
	}
	for i := 0; i < len(src)/2; i++ {
		dest[i] = int16(src[i*2]) | (int16(src[i*2+1]) << 8)
	}
	return nil
}

// float32SliceToByteSlice converts a float32 slice to a little-endian byte slice.
// This uses unsafe, assuming a direct bitwise representation matches Wasm float representation.
// For maximum portability, encoding/binary with Float32bits and LittleEndian should be used.
func float32SliceToByteSlice(s []float32) []byte {
	b := make([]byte, len(s)*4)
	for i, v_float := range s {
		u := *(*uint32)(unsafe.Pointer(&v_float)) // Reinterpret float32 as uint32 bits
		b[i*4+0] = byte(u)
		b[i*4+1] = byte(u >> 8)
		b[i*4+2] = byte(u >> 16)
		b[i*4+3] = byte(u >> 24)
	}
	return b
}

// float32SliceFromByteSlice converts a little-endian byte slice to a float32 slice.
// This uses unsafe, assuming a direct bitwise representation matches Wasm float representation.
// For maximum portability, encoding/binary with Float32frombits and LittleEndian should be used.
func float32SliceFromByteSlice(src []byte, dest []float32) error {
	if len(src)%4 != 0 {
		return fmt.Errorf("byte slice length %d is not a multiple of 4 for float32 conversion", len(src))
	}
	if len(dest)*4 < len(src) {
		return fmt.Errorf("destination float32 slice too small (len %d) for byte slice (len %d)", len(dest), len(src))
	}
	for i := 0; i < len(src)/4; i++ {
		u := uint32(src[i*4+0]) | uint32(src[i*4+1])<<8 | uint32(src[i*4+2])<<16 | uint32(src[i*4+3])<<24
		dest[i] = *(*float32)(unsafe.Pointer(&u))
	}
	return nil
}
