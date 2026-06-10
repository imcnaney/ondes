cmake_minimum_required(VERSION 3.15)
project(ondes_c C)

set(CMAKE_C_STANDARD 11)
set(CMAKE_C_STANDARD_REQUIRED ON)
# Keep GNU extensions on (default) for M_PI, strdup, dirent, etc.

if(NOT CMAKE_BUILD_TYPE)
  set(CMAKE_BUILD_TYPE Release)
endif()

add_compile_options(-Wall -Wextra)

# Engine + components + patch + I/O, everything under src/ except the CLI
# entry points in src/cmd/.
file(GLOB_RECURSE LIB_SOURCES CONFIGURE_DEPENDS ${CMAKE_SOURCE_DIR}/src/*.c)
list(FILTER LIB_SOURCES EXCLUDE REGEX "/src/cmd/")

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

# Unit tests.
enable_testing()
add_executable(ondes_tests ${CMAKE_SOURCE_DIR}/test/test_main.c)
target_link_libraries(ondes_tests PRIVATE ondes)
add_test(NAME unit COMMAND ondes_tests)
