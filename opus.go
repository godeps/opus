// Copyright © Go Opus Authors (see AUTHORS file)
//
// License for use of this code is detailed in the LICENSE file

package opus

import (
	"context"
	"fmt"
	"log"

	"github.com/tetratelabs/wazero/api"
)

type Application int

const (
	// AppVoIP is for voice over IP.
	AppVoIP = Application(2048) // OPUS_APPLICATION_VOIP
	// AppAudio is for general audio.
	AppAudio = Application(2049) // OPUS_APPLICATION_AUDIO
	// AppLowdelay is for low latency.
	AppRestrictedLowdelay = Application(2051) // OPUS_APPLICATION_RESTRICTED_LOWDELAY
)

const (
	xMAX_BITRATE       = 48000
	xMAX_FRAME_SIZE_MS = 60
	xMAX_FRAME_SIZE    = xMAX_BITRATE * xMAX_FRAME_SIZE_MS / 1000
	// Maximum size of an encoded frame. I actually have no idea, but this
	// looks like it's big enough.
	maxEncodedFrameSize = 10000
)

func Version() string {
	ctx := context.Background() // Context for initialization
	wctx, err := GetWasmContext(ctx)
	opusGetVersionString := wctx.module.ExportedFunction("opus_get_version_string")

	if err != nil {
		log.Fatalf("Failed to get wasm context for decoder: %v", err)
	}

	results, err := opusGetVersionString.Call(ctx)
	if err != nil {
		log.Fatalf("Failed to call opus_get_version_string: %v", err)
	}

	ptrVersion := results[0]
	version, err := readCString(wctx.module.Memory(), uint32(ptrVersion))
	if err != nil {
		log.Fatalf("Failed to read version string: %v", err)
	}

	return version
}

func readCString(memory api.Memory, offset uint32) (string, error) {
	var buffer []byte
	for {
		b, ok := memory.ReadByte(offset)
		if !ok {
			return "", fmt.Errorf("failed to read byte at offset %d", offset)
		}
		if b == 0 {
			break
		}
		buffer = append(buffer, b)
		offset++
	}
	return string(buffer), nil
}
