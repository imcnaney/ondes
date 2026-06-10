// audiodev wraps miniaudio playback-device discovery and a running output
// stream for the live synth. Only the live tools link it; the offline
// renderer stays dependency-free. (The C analogue of the Go audiodev pkg.)
#ifndef ONDES_AUDIODEV_H
#define ONDES_AUDIODEV_H

#include <stddef.h>

typedef struct AudioOut AudioOut;

// AudioRenderFn fills `frames` interleaved stereo float32 samples into out.
// Called on the real-time audio thread.
typedef void (*AudioRenderFn)(float *out, unsigned frames, void *user);

// audio_list_outputs prints the available playback devices. Returns 0 on
// success.
int audio_list_outputs(void);

// audio_open starts a playback stream on the device whose name contains
// substr (case-insensitive; empty/NULL = system default). cb is invoked on
// the audio thread to render each period. The human-readable device name is
// written to label. Returns NULL on failure with a message in err.
AudioOut *audio_open(const char *substr, int sample_rate, int buffer_frames,
                     AudioRenderFn cb, void *user, char *label, size_t labellen,
                     char *err, size_t errlen);

void audio_close(AudioOut *a);

#endif // ONDES_AUDIODEV_H
