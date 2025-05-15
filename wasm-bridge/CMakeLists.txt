cmake_minimum_required(VERSION 3.16)
project(wasm_bridge LANGUAGES C CXX)

# Specify the toolchain file
set(CMAKE_TOOLCHAIN_FILE ${CMAKE_CURRENT_SOURCE_DIR}/wasm32-wasi-zig.cmake)

# Add include directories
include_directories(${CMAKE_CURRENT_SOURCE_DIR}/../opus/include)

# Find source files
file(GLOB WASM_BRIDGE_SOURCES "${CMAKE_CURRENT_SOURCE_DIR}/src/*.c")

# Add an executable target
add_executable(wasm_bridge ${WASM_BRIDGE_SOURCES})

# Link against libopus.a
target_link_libraries(wasm_bridge PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/lib/libopus.a)

set(EXPORT_FLAGS
  "-Wl,-zstack-size=2097152"
  "-Wl,--export=opus_get_version_string"
  "-Wl,--export=opus_encoder_get_size"
  "-Wl,--export=opus_encoder_init"
  "-Wl,--export=opus_encode"
  "-Wl,--export=opus_encode_float"
  "-Wl,--export=opus_encoder_ctl"
  "-Wl,--export=opus_strerror"
  "-Wl,--export=opus_decoder_get_size"
  "-Wl,--export=opus_decoder_init"
  "-Wl,--export=opus_decode"
  "-Wl,--export=opus_decode_float"
  "-Wl,--export=malloc"
  "-Wl,--export=free"
)

target_link_options(wasm_bridge PRIVATE -mexec-model=reactor -Wl,--no-entry ${EXPORT_FLAGS})
