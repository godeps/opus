#include <opus.h>
#include "export.h"

const int opus_bandwidth_narrowband = OPUS_BANDWIDTH_NARROWBAND;
EXPORT(get_opus_bandwidth_narrowband_address) const int* get_opus_bandwidth_narrowband_address() { return &opus_bandwidth_narrowband; }

const int opus_bandwidth_mediumband = OPUS_BANDWIDTH_MEDIUMBAND;
EXPORT(get_opus_bandwidth_mediumband_address) const int* get_opus_bandwidth_mediumband_address() { return &opus_bandwidth_mediumband; }

const int opus_bandwidth_wideband = OPUS_BANDWIDTH_WIDEBAND;
EXPORT(get_opus_bandwidth_wideband_address) const int* get_opus_bandwidth_wideband_address() { return &opus_bandwidth_wideband; }

const int opus_bandwidth_superwideband = OPUS_BANDWIDTH_SUPERWIDEBAND;
EXPORT(get_opus_bandwidth_superwideband_address) const int* get_opus_bandwidth_superwideband_address() { return &opus_bandwidth_superwideband; }

const int opus_bandwidth_fullband = OPUS_BANDWIDTH_FULLBAND;
EXPORT(get_opus_bandwidth_fullband_address) const int* get_opus_bandwidth_fullband_address() { return &opus_bandwidth_fullband; }

const int opus_auto = OPUS_AUTO;
EXPORT(get_opus_auto_address) const int* get_opus_auto_address() { return &opus_auto; }

const int opus_bitrate_max = OPUS_BITRATE_MAX;
EXPORT(get_opus_bitrate_max_address) const int* get_opus_bitrate_max_address() { return &opus_bitrate_max; }

const int opus_ok = OPUS_OK;
EXPORT(get_opus_ok_address) const int* get_opus_ok_address() { return &opus_ok; }

const int opus_bad_arg = OPUS_BAD_ARG;
EXPORT(get_opus_bad_arg_address) const int* get_opus_bad_arg_address() { return &opus_bad_arg; }

const int opus_buffer_too_small = OPUS_BUFFER_TOO_SMALL;
EXPORT(get_opus_buffer_too_small_address) const int* get_opus_buffer_too_small_address() { return &opus_buffer_too_small; }

const int opus_internal_error = OPUS_INTERNAL_ERROR;
EXPORT(get_opus_internal_error_address) const int* get_opus_internal_error_address() { return &opus_internal_error; }

const int opus_invalid_packet = OPUS_INVALID_PACKET;
EXPORT(get_opus_invalid_packet_address) const int* get_opus_invalid_packet_address() { return &opus_invalid_packet; }

const int opus_unimplemented = OPUS_UNIMPLEMENTED;
EXPORT(get_opus_unimplemented_address) const int* get_opus_unimplemented_address() { return &opus_unimplemented; }

const int opus_invalid_state = OPUS_INVALID_STATE;
EXPORT(get_opus_invalid_state_address) const int* get_opus_invalid_state_address() { return &opus_invalid_state; }

const int opus_alloc_fail = OPUS_ALLOC_FAIL;
EXPORT(get_opus_alloc_fail_address) const int* get_opus_alloc_fail_address() { return &opus_alloc_fail; }

const int opus_application_voip = OPUS_APPLICATION_VOIP;
EXPORT(get_opus_application_voip_address) const int* get_opus_application_voip_address() { return &opus_application_voip; }

const int opus_application_audio = OPUS_APPLICATION_AUDIO;
EXPORT(get_opus_application_audio_address) const int* get_opus_application_audio_address() { return &opus_application_audio; }

const int opus_application_restricted_lowdelay = OPUS_APPLICATION_RESTRICTED_LOWDELAY;
EXPORT(get_opus_application_restricted_lowdelay_address) const int* get_opus_application_restricted_lowdelay_address() { return &opus_application_restricted_lowdelay; }
