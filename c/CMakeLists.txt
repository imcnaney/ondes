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

# Live MIDI -> audio synth (the `o` tool). macOS only: audio out via the
# vendored single-header miniaudio (CoreAudio), MIDI in via CoreMIDI - no
# third-party libraries to install. Other platforms would add an RtMidi /
# miniaudio-on-ALSA backend behind the same audiodev/mididev interfaces.
if(APPLE)
  add_executable(o
    ${CMAKE_SOURCE_DIR}/src/cmd/o.c
    ${CMAKE_SOURCE_DIR}/src/audiodev/audiodev.c
    ${CMAKE_SOURCE_DIR}/src/audiodev/miniaudio_impl.c
    ${CMAKE_SOURCE_DIR}/src/mididev/coremidi.c)
  target_include_directories(o PRIVATE ${CMAKE_SOURCE_DIR}/third_party/miniaudio)
  target_link_libraries(o PRIVATE ondes
    "-framework CoreFoundation" "-framework CoreAudio"
    "-framework AudioToolbox" "-framework AudioUnit"
    "-framework CoreMIDI")
  # miniaudio is a large vendored single header; don't subject it to -Wall.
  set_source_files_properties(
    ${CMAKE_SOURCE_DIR}/src/audiodev/miniaudio_impl.c
    PROPERTIES COMPILE_OPTIONS "-w")
endif()

# Unit tests.
enable_testing()
add_executable(ondes_tests ${CMAKE_SOURCE_DIR}/test/test_main.c)
target_link_libraries(ondes_tests PRIVATE ondes)
add_test(NAME unit COMMAND ondes_tests)
