#include <opus.h>
#include "export.h"

EXPORT(bridge_decoder_get_last_packet_duration)
int
bridge_decoder_get_last_packet_duration(OpusDecoder *st, opus_int32 *samples)
{
	return opus_decoder_ctl(st, OPUS_GET_LAST_PACKET_DURATION(samples));
}
