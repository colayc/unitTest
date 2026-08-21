add_custom_target(
  known-generated ALL
  COMMAND "${CMAKE_COMMAND}" -E touch "${CMAKE_CURRENT_BINARY_DIR}/known-generated.stamp"
  VERBATIM
)
