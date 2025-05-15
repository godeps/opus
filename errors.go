// Copyright © Go Opus Authors (see AUTHORS file)
//
// License for use of this code is detailed in the LICENSE file

package opus

import (
	"context" // Needed for GetWasmContext
	"fmt"
	"log" // Add log for potential errors getting context
)

type Error int

var _ error = Error(0)

// Libopus errors using integer values corresponding to OPUS_*.
const (
	ErrOK             = Error(0)  // OPUS_OK
	ErrBadArg         = Error(-1) // OPUS_BAD_ARG
	ErrBufferTooSmall = Error(-2) // OPUS_BUFFER_TOO_SMALL
	ErrInternalError  = Error(-3) // OPUS_INTERNAL_ERROR
	ErrInvalidPacket  = Error(-4) // OPUS_INVALID_PACKET
	ErrUnimplemented  = Error(-5) // OPUS_UNIMPLEMENTED
	ErrInvalidState   = Error(-6) // OPUS_INVALID_STATE
	ErrAllocFail      = Error(-7) // OPUS_ALLOC_FAIL
)

// Error string (in human readable format) for libopus errors using wazero.
func (e Error) Error() string {
	ctx := context.Background()
	wctx, err := GetWasmContext(ctx) // Assuming GetWasmContext is accessible
	if err != nil {
		// Handle error getting context, perhaps return a default error string
		log.Printf("Failed to get wasm context for strerror: %v", err)
		return fmt.Sprintf("opus: error getting WASM context (%d)", e)
	}

	opusStrError := wctx.module.ExportedFunction("opus_strerror")
	if opusStrError == nil {
		// Handle case where function is not exported
		log.Printf("opus_strerror function not found in WASM module")
		return fmt.Sprintf("opus: opus_strerror not available in WASM (%d)", e)
	}

	// Call the WASM function
	results, err := opusStrError.Call(ctx, uint64(e)) // Pass error code as uint64
	if err != nil {
		log.Printf("Failed to call opus_strerror: %v", err)
		return fmt.Sprintf("opus: failed calling opus_strerror (%d)", e)
	}

	// The result is a pointer to a C string in WASM memory
	ptrErrorString := results[0]

	// Read the C string from WASM memory
	errorString, err := readCString(wctx.module.Memory(), uint32(ptrErrorString)) // Assuming readCString is accessible
	if err != nil {
		log.Printf("Failed to read error string from WASM memory: %v", err)
		return fmt.Sprintf("opus: failed reading error string from WASM (%d)", e)
	}

	return fmt.Sprintf("opus: %s", errorString)
}

// Need to make sure GetWasmContext and readCString are indeed accessible.
// They are in opus.go within the same package, so they should be.
// The code in opus.go shows they are package-level functions.
