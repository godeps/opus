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

// wasmContext holds the shared Wasm runtime and module.
type wasmContext struct {
	runtime wazero.Runtime
	module  api.Module
	malloc  api.Function
	free    api.Function
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
		// Create a new context for the init process if the provided one is nil or already cancelled
		initCtx := context.Background()

		rt := wazero.NewRuntime(initCtx)

		// Instantiate WASI, if your wasm module needs it.
		// Opus itself likely doesn't, but the C toolchain (like Emscripten) might add WASI imports.
		wasi_snapshot_preview1.MustInstantiate(initCtx, rt)

		compiledModule, err := rt.CompileModule(initCtx, wasmBinary)
		if err != nil {
			wasmInitErr = fmt.Errorf("failed to compile wasm module: %w", err)
			log.Printf("initWasm: %v", wasmInitErr)
			rt.Close(initCtx) // Clean up runtime
			return
		}

		// Instantiate the module. Use a name that indicates it's a global instance.
		// Configuration for memory: Opus might need a certain amount.
		// Default is 1 page (64KB). Check if Opus needs more or if it uses dynamic memory.
		// For simplicity, we'll use default memory config. If malloc is used, it should grow.
		cfg := wazero.NewModuleConfig().WithName("opus-global")
		mod, err := rt.InstantiateModule(initCtx, compiledModule, cfg)
		if err != nil {
			wasmInitErr = fmt.Errorf("failed to instantiate wasm module: %w", err)
			log.Printf("initWasm: %v", wasmInitErr)
			rt.Close(initCtx)             // Clean up runtime
			compiledModule.Close(initCtx) // Clean up compiled module
			return
		}

		mallocFn := mod.ExportedFunction("malloc")
		freeFn := mod.ExportedFunction("free")
		if mallocFn == nil || freeFn == nil {
			wasmInitErr = fmt.Errorf("malloc or free not exported from wasm module")
			log.Printf("initWasm: %v", wasmInitErr)
			rt.Close(initCtx)             // Clean up runtime
			compiledModule.Close(initCtx) // Clean up compiled module
			mod.Close(initCtx)            // Clean up module
			return
		}

		globalWasmContext = &wasmContext{
			runtime: rt,
			module:  mod,
			malloc:  mallocFn,
			free:    freeFn,
		}

		// Load constants from the module. This part might need to be dynamic
		// or rely on specific exported functions from the Wasm.
		// Example (assuming functions exist in the wasm module):
		if err := loadOpusConstants(initCtx, mod); err != nil {
			wasmInitErr = fmt.Errorf("failed to load opus constants from wasm: %w", err)
			log.Printf("initWasm: %v", wasmInitErr)
			// The module and runtime are part of globalWasmContext, which is not fully valid,
			// but we can rely on the finalizer or explicit Close if needed.
			// For now, just return the error state.
			return
		}
	})

	return wasmInitErr // Return the error from the Do block or nil
}

// mustReadInt32Constant reads an int32 constant from wasm memory via an exported getter function.
func mustReadInt32Constant(ctx context.Context, module api.Module, funcName string) int32 {
	fn := module.ExportedFunction(funcName)
	if fn == nil {
		log.Fatalf("Wasm function %s not found", funcName)
	}
	results, err := fn.Call(ctx)
	if err != nil {
		log.Fatalf("Failed to call %s: %v", funcName, err)
	}
	ptr := uint32(results[0])
	val, ok := module.Memory().ReadUint32Le(ptr)
	if !ok {
		log.Fatalf("Failed to read memory at %d for %s", ptr, funcName)
	}
	return int32(val)
}

// loadOpusConstants loads the Opus constants from the wasm module into global variables.
func loadOpusConstants(ctx context.Context, module api.Module) error {
	// This function needs to be populated with the actual constant loading logic
	// from encoder.go, using the provided 'module'.

	opusOk = mustReadInt32Constant(ctx, module, "get_opus_ok_address")
	opusBadArg = mustReadInt32Constant(ctx, module, "get_opus_bad_arg_address")
	opusBufferTooSmall = mustReadInt32Constant(ctx, module, "get_opus_buffer_too_small_address")
	opusInternalError = mustReadInt32Constant(ctx, module, "get_opus_internal_error_address")
	opusInvalidPacket = mustReadInt32Constant(ctx, module, "get_opus_invalid_packet_address")
	opusUnimplemented = mustReadInt32Constant(ctx, module, "get_opus_unimplemented_address")
	opusInvalidState = mustReadInt32Constant(ctx, module, "get_opus_invalid_state_address")
	opusAllocFail = mustReadInt32Constant(ctx, module, "get_opus_alloc_fail_address")

	opusBandwidthNarrowband = mustReadInt32Constant(ctx, module, "get_opus_bandwidth_narrowband_address")
	opusBandwidthMediumband = mustReadInt32Constant(ctx, module, "get_opus_bandwidth_mediumband_address")
	opusBandwidthWideband = mustReadInt32Constant(ctx, module, "get_opus_bandwidth_wideband_address")
	opusBandwidthSuperWideband = mustReadInt32Constant(ctx, module, "get_opus_bandwidth_superwideband_address")
	opusBandwidthFullband = mustReadInt32Constant(ctx, module, "get_opus_bandwidth_fullband_address")

	// Update Bandwidth variables
	Narrowband = Bandwidth(opusBandwidthNarrowband)
	Mediumband = Bandwidth(opusBandwidthMediumband)
	Wideband = Bandwidth(opusBandwidthWideband)
	SuperWideband = Bandwidth(opusBandwidthSuperWideband)
	Fullband = Bandwidth(opusBandwidthFullband)

	opusAuto = mustReadInt32Constant(ctx, module, "get_opus_auto_address")
	opusBitrateMax = mustReadInt32Constant(ctx, module, "get_opus_bitrate_max_address")

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
		globalWasmContext.malloc = nil
		globalWasmContext.free = nil
		globalWasmContext = nil    // Clear the global context
		wasmInitOnce = sync.Once{} // Reset the initOnce for potential re-init in tests etc.
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
	if byteCount == 0 {
		// For some Opus calls (like PLC), a 0-len data is valid with a NULL ptr.
		// This helper is for writing existing Go data. If data is non-nil but empty,
		// malloc(0) might be problematic or return non-NULL, but it's unusual to write 0 bytes this way.
		// The original check was specific to non-nil empty slices.
		// Let's assume if len(data) is 0, the caller intends this (e.g. for an empty buffer placeholder).
		// However, directly allocating for 0 bytes to write *into* is often an error.
		// Given this is writing *from* a Go slice, if the slice is empty, we allocate 0.
		// Many mallocs return NULL or a unique pointer for malloc(0). We'll proceed.
	}

	results, err := wc.malloc.Call(ctx, uint64(byteCount))
	if err != nil {
		return 0, fmt.Errorf("wasm malloc failed: %w", err)
	}
	ptr = uint32(results[0])
	if ptr == 0 && byteCount > 0 { // If malloc(0) returns 0, that might be valid. But not for byteCount > 0.
		return 0, fmt.Errorf("wasm malloc returned NULL for non-zero size")
	}
	if byteCount > 0 { // Only write if there's data to write
		if !wc.module.Memory().Write(ptr, data) {
			// Attempt to free if write failed
			if ptr != 0 { // Check ptr before calling free
				wc.free.Call(ctx, uint64(ptr))
			}
			return 0, fmt.Errorf("wasm memory write failed")
		}
	}
	return ptr, nil
}

// allocateInt32Ptr allocates memory for an int32 in wasm memory using the wasmContext's malloc.
func (wc *wasmContext) allocateInt32Ptr(ctx context.Context) (ptr uint32, err error) {
	results, err := wc.malloc.Call(ctx, 4) // sizeof(int32) is 4
	if err != nil {
		return 0, fmt.Errorf("wasm malloc for int32 ptr failed: %w", err)
	}
	ptr = uint32(results[0])
	if ptr == 0 {
		return 0, fmt.Errorf("wasm malloc for int32 ptr returned NULL")
	}
	return ptr, nil
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
