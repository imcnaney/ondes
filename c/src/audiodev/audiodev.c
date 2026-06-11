#include "ondes/audiodev.h"

#include <ctype.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "miniaudio.h"

// ci_contains: case-insensitive substring test. (strcasestr is not portable
// - notably absent on Windows/MinGW - so we roll our own.)
static int ci_contains(const char *hay, const char *needle) {
    if (!needle || !*needle) return 1;
    size_t nl = strlen(needle);
    for (; *hay; hay++) {
        size_t i = 0;
        while (i < nl && hay[i] &&
               tolower((unsigned char)hay[i]) == tolower((unsigned char)needle[i]))
            i++;
        if (i == nl) return 1;
    }
    return 0;
}

struct AudioOut {
    ma_context ctx;
    ma_device device;
    AudioRenderFn cb;
    void *user;
};

static void data_callback(ma_device *dev, void *output, const void *input,
                          ma_uint32 frame_count) {
    (void)input;
    AudioOut *a = dev->pUserData;
    a->cb((float *)output, (unsigned)frame_count, a->user);
}

int audio_list_outputs(void) {
    ma_context ctx;
    if (ma_context_init(NULL, 0, NULL, &ctx) != MA_SUCCESS) {
        fprintf(stderr, "audio: failed to init context\n");
        return 1;
    }
    ma_device_info *playback;
    ma_uint32 n;
    if (ma_context_get_devices(&ctx, &playback, &n, NULL, NULL) != MA_SUCCESS) {
        ma_context_uninit(&ctx);
        fprintf(stderr, "audio: failed to enumerate devices\n");
        return 1;
    }
    printf("audio output devices:\n");
    for (ma_uint32 i = 0; i < n; i++)
        printf("  - %s%s\n", playback[i].name,
               playback[i].isDefault ? " (default)" : "");
    ma_context_uninit(&ctx);
    return 0;
}

AudioOut *audio_open(const char *substr, int sample_rate, int buffer_frames,
                     AudioRenderFn cb, void *user, char *label, size_t labellen,
                     char *err, size_t errlen) {
    AudioOut *a = calloc(1, sizeof(*a));
    a->cb = cb;
    a->user = user;

    if (ma_context_init(NULL, 0, NULL, &a->ctx) != MA_SUCCESS) {
        snprintf(err, errlen, "failed to init audio context");
        free(a);
        return NULL;
    }

    ma_device_id *dev_id = NULL;
    ma_device_id chosen;
    snprintf(label, labellen, "(system default)");
    if (substr && *substr) {
        ma_device_info *playback;
        ma_uint32 n;
        if (ma_context_get_devices(&a->ctx, &playback, &n, NULL, NULL) ==
            MA_SUCCESS) {
            for (ma_uint32 i = 0; i < n; i++) {
                if (ci_contains(playback[i].name, substr)) {
                    chosen = playback[i].id;
                    dev_id = &chosen;
                    snprintf(label, labellen, "%s", playback[i].name);
                    break;
                }
            }
        }
        if (!dev_id) {
            snprintf(err, errlen, "no audio output matched \"%s\"", substr);
            ma_context_uninit(&a->ctx);
            free(a);
            return NULL;
        }
    }

    ma_device_config cfg = ma_device_config_init(ma_device_type_playback);
    cfg.playback.format = ma_format_f32;
    cfg.playback.channels = 2;
    cfg.sampleRate = (ma_uint32)sample_rate;
    cfg.periodSizeInFrames = (ma_uint32)buffer_frames;
    cfg.playback.pDeviceID = dev_id;
    cfg.dataCallback = data_callback;
    cfg.pUserData = a;

    if (ma_device_init(&a->ctx, &cfg, &a->device) != MA_SUCCESS) {
        snprintf(err, errlen, "failed to open audio device");
        ma_context_uninit(&a->ctx);
        free(a);
        return NULL;
    }
    if (ma_device_start(&a->device) != MA_SUCCESS) {
        snprintf(err, errlen, "failed to start audio device");
        ma_device_uninit(&a->device);
        ma_context_uninit(&a->ctx);
        free(a);
        return NULL;
    }
    return a;
}

void audio_close(AudioOut *a) {
    if (!a) return;
    ma_device_uninit(&a->device);
    ma_context_uninit(&a->ctx);
    free(a);
}
