include_guard(GLOBAL)

if(CMAKE_VERSION VERSION_LESS "3.25")
  message(FATAL_ERROR "UnitTestIDE: CMake 3.25 or newer is required")
endif()

cmake_policy(PUSH)
cmake_policy(VERSION 3.25)

function(_utide_count_keyword output keyword)
  set(count 0)
  foreach(argument IN LISTS ARGN)
    if(argument STREQUAL keyword)
      math(EXPR count "${count} + 1")
    endif()
  endforeach()
  set("${output}" "${count}" PARENT_SCOPE)
endfunction()

function(_utide_require_keyword_once function_name keyword)
  _utide_count_keyword(count "${keyword}" ${ARGN})
  if(NOT count EQUAL 1)
    message(FATAL_ERROR
      "UnitTestIDE: ${function_name} requires ${keyword} exactly once"
    )
  endif()
endfunction()

function(_utide_validate_test_target function_name test_name target_name)
  if(test_name STREQUAL "")
    message(FATAL_ERROR "UnitTestIDE: ${function_name} requires a non-empty TEST")
  endif()
  string(LENGTH "${test_name}" test_name_bytes)
  if(test_name_bytes GREATER 512)
    message(FATAL_ERROR "UnitTestIDE: ${function_name} TEST exceeds 512 bytes")
  endif()
  if(target_name STREQUAL "")
    message(FATAL_ERROR "UnitTestIDE: ${function_name} requires a non-empty TARGET")
  endif()
  if(NOT TARGET "${target_name}")
    message(FATAL_ERROR
      "UnitTestIDE: ${function_name} TARGET ${target_name} does not exist"
    )
  endif()
  get_target_property(target_type "${target_name}" TYPE)
  if(NOT target_type STREQUAL "EXECUTABLE")
    message(FATAL_ERROR
      "UnitTestIDE: ${function_name} TARGET ${target_name} must be an executable target"
    )
  endif()

  string(SHA256 test_identity "${test_name}")
  get_property(registered GLOBAL PROPERTY _UTIDE_REGISTERED_TEST_IDENTITIES)
  if(TEST "${test_name}" OR test_identity IN_LIST registered)
    message(FATAL_ERROR
      "UnitTestIDE: CTest test ${test_name} already exists or is a duplicate"
    )
  endif()
  set(_UTIDE_PENDING_TEST_IDENTITY "${test_identity}" PARENT_SCOPE)
endfunction()

function(_utide_register_test test_name target_name labels)
  add_test(NAME "${test_name}" COMMAND "$<TARGET_FILE:${target_name}>")
  set_tests_properties("${test_name}" PROPERTIES LABELS "${labels}")
  set_property(
    GLOBAL APPEND
    PROPERTY _UTIDE_REGISTERED_TEST_IDENTITIES "${_UTIDE_PENDING_TEST_IDENTITY}"
  )
endfunction()

function(_utide_json_member output json member)
  string(JSON value ERROR_VARIABLE json_error GET "${json}" "${member}")
  if(NOT json_error STREQUAL "NOTFOUND")
    message(FATAL_ERROR
      "UnitTestIDE: generator version JSON has no valid ${member}: ${json_error}"
    )
  endif()
  set("${output}" "${value}" PARENT_SCOPE)
endfunction()

function(_utide_resolve_generator output)
  set(expected_generator_version "1.0.0")
  set(expected_runner_protocol "utide.runner.v1")
  if(NOT DEFINED UTIDE_UNITY_RUNNER_GENERATOR OR
      UTIDE_UNITY_RUNNER_GENERATOR STREQUAL "")
    message(FATAL_ERROR
      "UnitTestIDE: UTIDE_UNITY_RUNNER_GENERATOR must name the product generator"
    )
  endif()
  if(NOT IS_ABSOLUTE "${UTIDE_UNITY_RUNNER_GENERATOR}")
    message(FATAL_ERROR
      "UnitTestIDE: UTIDE_UNITY_RUNNER_GENERATOR must be an absolute path"
    )
  endif()
  if(NOT EXISTS "${UTIDE_UNITY_RUNNER_GENERATOR}" OR
      IS_DIRECTORY "${UTIDE_UNITY_RUNNER_GENERATOR}")
    message(FATAL_ERROR
      "UnitTestIDE: UTIDE_UNITY_RUNNER_GENERATOR is unavailable"
    )
  endif()
  file(REAL_PATH "${UTIDE_UNITY_RUNNER_GENERATOR}" generator)

  get_property(
    validated_generator_set
    GLOBAL PROPERTY _UTIDE_VALIDATED_GENERATOR
    SET
  )
  if(validated_generator_set)
    get_property(validated_generator GLOBAL PROPERTY _UTIDE_VALIDATED_GENERATOR)
    if(NOT validated_generator STREQUAL generator)
      message(FATAL_ERROR
        "UnitTestIDE: one configure cannot use multiple Unity generators"
      )
    endif()
    set("${output}" "${generator}" PARENT_SCOPE)
    return()
  endif()

  execute_process(
    COMMAND "${generator}" "--version=json-v1"
    RESULT_VARIABLE version_result
    OUTPUT_VARIABLE version_output
    ERROR_VARIABLE version_error
    OUTPUT_STRIP_TRAILING_WHITESPACE
    ERROR_STRIP_TRAILING_WHITESPACE
    ENCODING UTF-8
    TIMEOUT 10
  )
  if(NOT version_result EQUAL 0)
    message(FATAL_ERROR
      "UnitTestIDE: Unity generator version probe failed (${version_result}): ${version_error}"
    )
  endif()
  string(JSON member_count ERROR_VARIABLE json_error LENGTH "${version_output}")
  if(NOT json_error STREQUAL "NOTFOUND" OR NOT member_count EQUAL 4)
    message(FATAL_ERROR "UnitTestIDE: generator version JSON has an invalid shape")
  endif()
  _utide_json_member(schema_version "${version_output}" "schemaVersion")
  _utide_json_member(generator_name "${version_output}" "name")
  _utide_json_member(generator_version "${version_output}" "version")
  _utide_json_member(runner_protocol "${version_output}" "runnerProtocol")
  if(NOT schema_version STREQUAL "1" OR
      NOT generator_name STREQUAL "unity-runner-generator" OR
      NOT generator_version STREQUAL expected_generator_version OR
      NOT runner_protocol STREQUAL expected_runner_protocol)
    message(FATAL_ERROR
      "UnitTestIDE: Unity generator must be version ${expected_generator_version} with ${expected_runner_protocol}"
    )
  endif()

  set_property(GLOBAL PROPERTY _UTIDE_VALIDATED_GENERATOR "${generator}")
  set("${output}" "${generator}" PARENT_SCOPE)
endfunction()

function(_utide_assert_unity_target_without_main target_name)
  get_target_property(imported "${target_name}" IMPORTED)
  get_target_property(aliased_target "${target_name}" ALIASED_TARGET)
  if(imported OR NOT aliased_target STREQUAL "aliased_target-NOTFOUND")
    message(FATAL_ERROR
      "UnitTestIDE: Unity TARGET must be a non-imported, non-alias executable target"
    )
  endif()
  get_target_property(attached "${target_name}" _UTIDE_UNITY_RUNNER_ATTACHED)
  if(attached)
    message(FATAL_ERROR
      "UnitTestIDE: Unity TARGET ${target_name} already has a generated runner"
    )
  endif()

  get_target_property(target_source_dir "${target_name}" SOURCE_DIR)
  get_target_property(target_sources "${target_name}" SOURCES)
  foreach(source IN LISTS target_sources)
    if(source MATCHES "^\\$<")
      message(FATAL_ERROR
        "UnitTestIDE: cannot verify Unity TARGET main ownership through generator-expression sources"
      )
    endif()
    if(IS_ABSOLUTE "${source}")
      set(source_path "${source}")
    else()
      get_filename_component(
        source_path "${source}"
        ABSOLUTE BASE_DIR "${target_source_dir}"
      )
    endif()
    if(EXISTS "${source_path}" AND NOT IS_DIRECTORY "${source_path}")
      file(SIZE "${source_path}" source_size)
      if(source_size GREATER 4194304)
        message(FATAL_ERROR
          "UnitTestIDE: cannot verify Unity TARGET source larger than 4 MiB"
        )
      endif()
      file(READ "${source_path}" source_content LIMIT 4194305)
      string(REGEX MATCH
        "(^|[\r\n])[ \t]*(int|void)[ \t\r\n]+main[ \t\r\n]*\\("
        main_declaration
        "${source_content}"
      )
      if(NOT main_declaration STREQUAL "")
        message(FATAL_ERROR
          "UnitTestIDE: Unity TARGET ${target_name} already has a main declaration"
        )
      endif()
    endif()
  endforeach()
endfunction()

function(_utide_resolve_sources output)
  file(REAL_PATH "${CMAKE_SOURCE_DIR}" source_root)
  set(resolved)
  set(dependencies)
  list(LENGTH ARGN declared_source_count)
  if(declared_source_count GREATER 256)
    message(FATAL_ERROR
      "UnitTestIDE: TEST_SOURCES exceeds the 256-source limit"
    )
  endif()
  foreach(source IN LISTS ARGN)
    if(source STREQUAL "" OR source MATCHES "^\\$<")
      message(FATAL_ERROR
        "UnitTestIDE: TEST_SOURCES must contain concrete source paths"
      )
    endif()
    file(
      REAL_PATH "${source}" source_path
      BASE_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}"
    )
    if(NOT EXISTS "${source_path}" OR IS_DIRECTORY "${source_path}")
      message(FATAL_ERROR
        "UnitTestIDE: TEST_SOURCE ${source} is not an existing file"
      )
    endif()
    file(RELATIVE_PATH relative_source "${source_root}" "${source_path}")
    if(IS_ABSOLUTE "${relative_source}" OR
        relative_source STREQUAL ".." OR
        relative_source MATCHES "^\\.\\./")
      message(FATAL_ERROR
        "UnitTestIDE: TEST_SOURCE ${source} escapes the CMake source root"
      )
    endif()
    file(TO_CMAKE_PATH "${relative_source}" portable_source)
    list(APPEND resolved "${portable_source}")
    list(APPEND dependencies "${source_path}")
  endforeach()
  list(LENGTH resolved source_count)
  if(source_count EQUAL 0)
    message(FATAL_ERROR "UnitTestIDE: TEST_SOURCES requires at least one source")
  endif()
  list(LENGTH resolved unique_count_before)
  list(REMOVE_DUPLICATES resolved)
  list(LENGTH resolved unique_count_after)
  if(NOT unique_count_before EQUAL unique_count_after)
    message(FATAL_ERROR "UnitTestIDE: TEST_SOURCES contains a duplicate source")
  endif()
  set("${output}" "${resolved}" PARENT_SCOPE)
  set(_UTIDE_SOURCE_DEPENDENCIES "${dependencies}" PARENT_SCOPE)
  set(_UTIDE_SOURCE_ROOT "${source_root}" PARENT_SCOPE)
endfunction()

function(unit_test_ide_add_cpputest)
  _utide_require_keyword_once(
    "unit_test_ide_add_cpputest" "TEST" ${ARGV}
  )
  _utide_require_keyword_once(
    "unit_test_ide_add_cpputest" "TARGET" ${ARGV}
  )
  cmake_parse_arguments(PARSE_ARGV 0 argument "" "TEST;TARGET" "")
  if(DEFINED argument_UNPARSED_ARGUMENTS)
    message(FATAL_ERROR
      "UnitTestIDE: unit_test_ide_add_cpputest has unparsed arguments: ${argument_UNPARSED_ARGUMENTS}"
    )
  endif()
  if(DEFINED argument_KEYWORDS_MISSING_VALUES)
    message(FATAL_ERROR
      "UnitTestIDE: unit_test_ide_add_cpputest has keywords missing values: ${argument_KEYWORDS_MISSING_VALUES}"
    )
  endif()
  _utide_validate_test_target(
    "unit_test_ide_add_cpputest" "${argument_TEST}" "${argument_TARGET}"
  )
  _utide_register_test(
    "${argument_TEST}" "${argument_TARGET}" "utide.framework.cpputest"
  )
endfunction()

function(unit_test_ide_add_unity_test)
  foreach(keyword IN ITEMS TEST TARGET TEST_SOURCES)
    _utide_require_keyword_once(
      "unit_test_ide_add_unity_test" "${keyword}" ${ARGV}
    )
  endforeach()
  cmake_parse_arguments(
    PARSE_ARGV 0 argument
    ""
    "TEST;TARGET"
    "TEST_SOURCES"
  )
  if(DEFINED argument_UNPARSED_ARGUMENTS)
    message(FATAL_ERROR
      "UnitTestIDE: unit_test_ide_add_unity_test has unparsed arguments: ${argument_UNPARSED_ARGUMENTS}"
    )
  endif()
  if(DEFINED argument_KEYWORDS_MISSING_VALUES)
    message(FATAL_ERROR
      "UnitTestIDE: unit_test_ide_add_unity_test has keywords missing values: ${argument_KEYWORDS_MISSING_VALUES}"
    )
  endif()

  _utide_validate_test_target(
    "unit_test_ide_add_unity_test" "${argument_TEST}" "${argument_TARGET}"
  )
  _utide_assert_unity_target_without_main("${argument_TARGET}")
  _utide_resolve_sources(sources ${argument_TEST_SOURCES})
  _utide_resolve_generator(generator)

  string(SHA256 test_identity "${argument_TEST}")
  set(output_relative ".unit-test-ide/${test_identity}")
  set(runner_relative "${output_relative}/runner.c")
  set(manifest_relative "${output_relative}/manifest.json")
  file(REAL_PATH "${CMAKE_BINARY_DIR}" build_root)
  file(MAKE_DIRECTORY "${build_root}/${output_relative}")

  set(generator_arguments
    "generate"
    "--workspace-root" "${_UTIDE_SOURCE_ROOT}"
    "--build-root" "${build_root}"
    "--runner" "${runner_relative}"
    "--manifest" "${manifest_relative}"
  )
  foreach(source IN LISTS sources)
    list(APPEND generator_arguments "--source" "${source}")
  endforeach()
  execute_process(
    COMMAND "${generator}" ${generator_arguments}
    RESULT_VARIABLE generate_result
    OUTPUT_VARIABLE generate_output
    ERROR_VARIABLE generate_error
    OUTPUT_STRIP_TRAILING_WHITESPACE
    ERROR_STRIP_TRAILING_WHITESPACE
    ENCODING UTF-8
    TIMEOUT 30
  )
  if(NOT generate_result EQUAL 0)
    message(FATAL_ERROR
      "UnitTestIDE: Unity runner generation failed (${generate_result}): ${generate_error}"
    )
  endif()

  set(runner "${build_root}/${runner_relative}")
  set(manifest "${build_root}/${manifest_relative}")
  if(NOT EXISTS "${runner}" OR IS_DIRECTORY "${runner}" OR
      NOT EXISTS "${manifest}" OR IS_DIRECTORY "${manifest}")
    message(FATAL_ERROR
      "UnitTestIDE: Unity generator did not publish runner and manifest"
    )
  endif()
  set_source_files_properties("${runner}" PROPERTIES GENERATED TRUE)
  target_sources("${argument_TARGET}" PRIVATE "${runner}")
  set_property(
    TARGET "${argument_TARGET}"
    PROPERTY _UTIDE_UNITY_RUNNER_ATTACHED TRUE
  )
  set_property(
    DIRECTORY APPEND
    PROPERTY CMAKE_CONFIGURE_DEPENDS
    "${generator}" ${_UTIDE_SOURCE_DEPENDENCIES}
  )
  _utide_register_test(
    "${argument_TEST}"
    "${argument_TARGET}"
    "utide.framework.unity;utide.runner.v1"
  )
endfunction()

cmake_policy(POP)
