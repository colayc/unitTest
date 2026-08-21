add_library(unknown STATIC unknown.cpp)
add_custom_command(
  TARGET unknown POST_BUILD
  COMMAND unknown.exe --escape-offline-boundary
  VERBATIM
)
