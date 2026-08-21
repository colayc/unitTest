add_library(known STATIC known.cpp)
add_custom_command(
  TARGET known POST_BUILD
  COMMAND "${CMAKE_COMMAND}" -E touch "${CMAKE_CURRENT_BINARY_DIR}/known.stamp"
  VERBATIM
)
