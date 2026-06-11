# Plain-make build for the C port - an alternative to CMake for anyone who
# doesn't have (or doesn't want to fight) a working cmake. No dependencies
# beyond a C compiler; the offline tools need nothing else, and the live
# tool `o` links the macOS system frameworks (CoreAudio/CoreMIDI).
#
#   make            # build everything (offline tools + tests + benches,
#                   #   plus the live `o` synth on macOS)
#   make p          # just the MIDI-file -> WAV renderer
#   make o          # just the live synth (macOS)
#   make test       # build and run the unit tests
#   make compile_commands.json   # editor IntelliSense database (clangd / VS Code)
#   make clean
#
# Binaries land in build/ (same place CMake puts them), so the regression
# scripts (regression/check_c.sh etc.) work whichever build you use.

CC      ?= cc
CFLAGS  ?= -std=gnu11 -O3 -Wall -Wextra
CPPFLAGS := -Iinclude -Isrc/component
LDLIBS  := -lm

BUILD := build
OBJ   := $(BUILD)/obj

# Engine library: everything under src/ except the CLI mains (src/cmd) and
# the live device layers (src/audiodev, src/mididev).
LIB_SRC := $(wildcard src/synth/*.c src/component/*.c src/patch/*.c \
                      src/audio/*.c src/midi/*.c src/util/*.c)
LIB_OBJ := $(LIB_SRC:%.c=$(OBJ)/%.o)

UNAME := $(shell uname)

BINS := $(BUILD)/p $(BUILD)/ondes_tests $(BUILD)/note_setup $(BUILD)/render_throughput
ifeq ($(UNAME),Darwin)
BINS += $(BUILD)/o
endif

.PHONY: all clean test
all: $(BINS)

# --- offline tools, tests, benches (portable, no extra deps) ---
$(BUILD)/p: src/cmd/p.c $(LIB_OBJ)
	$(CC) $(CFLAGS) $(CPPFLAGS) $^ $(LDLIBS) -o $@
$(BUILD)/ondes_tests: test/test_main.c $(LIB_OBJ)
	$(CC) $(CFLAGS) $(CPPFLAGS) $^ $(LDLIBS) -o $@
$(BUILD)/note_setup: bench/note_setup.c $(LIB_OBJ)
	$(CC) $(CFLAGS) $(CPPFLAGS) $^ $(LDLIBS) -o $@
$(BUILD)/render_throughput: bench/render_throughput.c $(LIB_OBJ)
	$(CC) $(CFLAGS) $(CPPFLAGS) $^ $(LDLIBS) -o $@

# --- live synth (macOS): miniaudio (audio out) + CoreMIDI (MIDI in) ---
MA_INC := -Ithird_party/miniaudio
FRAMEWORKS := -framework CoreFoundation -framework CoreAudio \
              -framework AudioToolbox -framework AudioUnit -framework CoreMIDI
$(BUILD)/o: src/cmd/o.c src/audiodev/audiodev.c src/audiodev/miniaudio_impl.c \
            src/mididev/coremidi.c $(LIB_OBJ)
	$(CC) $(CFLAGS) $(CPPFLAGS) $(MA_INC) $^ $(LDLIBS) $(FRAMEWORKS) -o $@

# --- library objects ---
$(OBJ)/%.o: %.c
	@mkdir -p $(dir $@)
	$(CC) $(CFLAGS) $(CPPFLAGS) -c $< -o $@

test: $(BUILD)/ondes_tests
	./$(BUILD)/ondes_tests

# --- editor IntelliSense database (fixes "undefined include" squiggles in
# VS Code / clangd, including <stdatomic.h>, by recording the C11 flag and
# include paths). Consumed by clangd automatically, or point the VS Code
# C/C++ extension's "compileCommands" setting at it. ---
DB_SRC := $(LIB_SRC) src/cmd/p.c test/test_main.c bench/note_setup.c \
          bench/render_throughput.c
ifeq ($(UNAME),Darwin)
DB_SRC += src/cmd/o.c src/audiodev/audiodev.c src/audiodev/miniaudio_impl.c \
          src/mididev/coremidi.c
endif
.PHONY: compile_commands.json
compile_commands.json:
	@echo '[' > $@
	@first=1; for f in $(DB_SRC); do \
	  [ $$first -eq 1 ] || echo ',' >> $@; first=0; \
	  printf '  {"directory": "%s", "file": "%s", "command": "%s %s %s -c %s"}' \
	    "$(CURDIR)" "$$f" "$(CC) $(CFLAGS)" "$(CPPFLAGS)" "$(MA_INC)" "$$f" >> $@; \
	done; echo '' >> $@; echo ']' >> $@
	@echo "wrote $@ ($(words $(DB_SRC)) entries)"

clean:
	rm -rf $(BUILD) compile_commands.json
