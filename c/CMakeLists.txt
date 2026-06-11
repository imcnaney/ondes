cmake_minimum_required(VERSION 3.15)
project(ondes_c C)

set(CMAKE_C_STANDARD 11)
set(CMAKE_C_STANDARD_REQUIRED ON)
# Keep GNU extensions on (default) for M_PI, strdup, dirent, etc.

if(NOT CMAKE_BUILD_TYPE)
  set(CMAKE_BUILD_TYPE Release)
endif()

add_compile_options(-Wall -Wextra)

# Engine + components + patch + I/O. Everything under src/ except the CLI
# entry points (src/cmd/) and the live device layers (src/audiodev,
# src/mididev), which pull in miniaudio / CoreMIDI and so are kept out of
# the dependency-free offline library.
file(GLOB_RECURSE LIB_SOURCES CONFIGURE_DEPENDS ${CMAKE_SOURCE_DIR}/src/*.c)
list(FILTER LIB_SOURCES EXCLUDE REGEX "/src/cmd/")
list(FILTER LIB_SOURCES EXCLUDE REGEX "/src/audiodev/")
list(FILTER LIB_SOURCES EXCLUDE REGEX "/src/mididev/")

add_library(ondes STATIC ${LIB_SOURCES})
target_include_directories(ondes
  PUBLIC ${CMAKE_SOURCE_DIR}/include
  PRIVATE ${CMAKE_SOURCE_DIR}/src/component)

find_library(M_LIB m)
if(M_LIB)
  target_link_libraries(ondes PUBLIC ${M_LIB})
endif()

# MIDI-file -> WAV renderer (the `p` tool; pure C, no device deps).
add_executable(p ${CMAKE_SOURCE_DIR}/src/cmd/p.c)
target_link_libraries(p PRIVATE ondes)

# Live MIDI -> audio synth (the `o` tool). Audio out is the vendored
# single-header miniaudio (CoreAudio on macOS, WASAPI on Windows); MIDI in
# is a per-platform backend behind mididev.h - CoreMIDI on macOS, winmm on
# Windows - so no third-party MIDI library is needed. Linux would add an
# ALSA backend behind the same interface.
if(APPLE OR WIN32)
  if(APPLE)
    set(ONDES_MIDI_SRC ${CMAKE_SOURCE_DIR}/src/mididev/coremidi.c)
  else()
    set(ONDES_MIDI_SRC ${CMAKE_SOURCE_DIR}/src/mididev/winmm.c)
  endif()
  add_executable(o
    ${CMAKE_SOURCE_DIR}/src/cmd/o.c
    ${CMAKE_SOURCE_DIR}/src/audiodev/audiodev.c
    ${CMAKE_SOURCE_DIR}/src/audiodev/miniaudio_impl.c
    ${ONDES_MIDI_SRC})
  target_include_directories(o PRIVATE ${CMAKE_SOURCE_DIR}/third_party/miniaudio)
  if(APPLE)
    target_link_libraries(o PRIVATE ondes
      "-framework CoreFoundation" "-framework CoreAudio"
      "-framework AudioToolbox" "-framework AudioUnit"
      "-framework CoreMIDI")
  else() # WIN32
    # winmm for MIDI in; ole32 for miniaudio's WASAPI backend.
    target_link_libraries(o PRIVATE ondes winmm ole32)
  endif()
  # miniaudio is a large vendored single header; don't subject it to -Wall.
  set_source_files_properties(
    ${CMAKE_SOURCE_DIR}/src/audiodev/miniaudio_impl.c
    PROPERTIES COMPILE_OPTIONS "-w")
endif()

# Benchmarks: per-note setup (fresh vs pool) and per-sample render throughput.
add_executable(note_setup ${CMAKE_SOURCE_DIR}/bench/note_setup.c)
target_link_libraries(note_setup PRIVATE ondes)
add_executable(render_throughput ${CMAKE_SOURCE_DIR}/bench/render_throughput.c)
target_link_libraries(render_throughput PRIVATE ondes)

# Unit tests.
enable_testing()
add_executable(ondes_tests ${CMAKE_SOURCE_DIR}/test/test_main.c)
target_link_libraries(ondes_tests PRIVATE ondes)
add_test(NAME unit COMMAND ondes_tests)
