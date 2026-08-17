package unityrunner

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const runnerTypesTemplate = `
#include "unity.h"

#include <stdio.h>
#include <string.h>
#include <time.h>

typedef void (*utide_test_function)(void);

struct utide_case {
    const char *identity;
    const char *name;
    const char *source_path;
    unsigned long source_line;
    const char *arguments_json;
    utide_test_function function;
};

struct utide_options {
    const char *protocol;
    const char *mode;
    const char *case_identity;
    const char *result_path;
};

enum {
    UTIDE_EXIT_USAGE = 64,
    UTIDE_EXIT_PROTOCOL = 65,
    UTIDE_EXIT_CASE_NOT_FOUND = 66,
    UTIDE_EXIT_RESULT_OPEN = 73,
    UTIDE_EXIT_RESULT_WRITE = 74
};
`

const runnerRuntimeTemplate = `
static int utide_write_json_string(FILE *result, const char *value)
{
    const unsigned char *cursor = (const unsigned char *)value;
    if (fputc('"', result) == EOF) {
        return -1;
    }
    while (*cursor != 0U) {
        switch (*cursor) {
        case '"':
            if (fputs("\\\"", result) == EOF) return -1;
            break;
        case '\\':
            if (fputs("\\\\", result) == EOF) return -1;
            break;
        case '\b':
            if (fputs("\\b", result) == EOF) return -1;
            break;
        case '\f':
            if (fputs("\\f", result) == EOF) return -1;
            break;
        case '\n':
            if (fputs("\\n", result) == EOF) return -1;
            break;
        case '\r':
            if (fputs("\\r", result) == EOF) return -1;
            break;
        case '\t':
            if (fputs("\\t", result) == EOF) return -1;
            break;
        default:
            if (*cursor < 0x20U || *cursor == 0x7fU) {
                if (fprintf(result, "\\u%04x", (unsigned int)*cursor) < 0) return -1;
            } else if (fputc((int)*cursor, result) == EOF) {
                return -1;
            }
            break;
        }
        ++cursor;
    }
    return fputc('"', result) == EOF ? -1 : 0;
}

static int utide_write_case_record(FILE *result, const struct utide_case *test_case)
{
    if (fputs("{\"magic\":\"unit-test-ide\",\"protocol\":", result) == EOF ||
        utide_write_json_string(result, utide_protocol) != 0 ||
        fputs(",\"record\":\"case\",\"suite\":", result) == EOF ||
        utide_write_json_string(result, test_case->source_path) != 0 ||
        fputs(",\"case\":", result) == EOF ||
        utide_write_json_string(result, test_case->name) != 0 ||
        fputs(",\"identity\":", result) == EOF ||
        utide_write_json_string(result, test_case->identity) != 0 ||
        fputs(",\"arguments\":", result) == EOF ||
        fputs(test_case->arguments_json, result) == EOF ||
        fputs(",\"source\":{\"path\":", result) == EOF ||
        utide_write_json_string(result, test_case->source_path) != 0 ||
        fprintf(result, ",\"line\":%lu}", test_case->source_line) < 0 ||
        fputs(",\"generatorVersion\":", result) == EOF ||
        utide_write_json_string(result, utide_generator_version) != 0 ||
        fputs(",\"manifestSha256\":", result) == EOF ||
        utide_write_json_string(result, utide_manifest_sha256) != 0 ||
        fputs("}\n", result) == EOF) {
        return -1;
    }
    return fflush(result) == 0 ? 0 : -1;
}

static int utide_write_result_record(
    FILE *result,
    const struct utide_case *test_case,
    const char *status,
    unsigned long long duration_nanoseconds)
{
    if (fputs("{\"magic\":\"unit-test-ide\",\"protocol\":", result) == EOF ||
        utide_write_json_string(result, utide_protocol) != 0 ||
        fputs(",\"record\":\"testFinished\",\"suite\":", result) == EOF ||
        utide_write_json_string(result, test_case->source_path) != 0 ||
        fputs(",\"case\":", result) == EOF ||
        utide_write_json_string(result, test_case->name) != 0 ||
        fputs(",\"identity\":", result) == EOF ||
        utide_write_json_string(result, test_case->identity) != 0 ||
        fputs(",\"arguments\":", result) == EOF ||
        fputs(test_case->arguments_json, result) == EOF ||
        fputs(",\"source\":{\"path\":", result) == EOF ||
        utide_write_json_string(result, test_case->source_path) != 0 ||
        fprintf(result, ",\"line\":%lu}", test_case->source_line) < 0 ||
        fputs(",\"status\":", result) == EOF ||
        utide_write_json_string(result, status) != 0 ||
        fprintf(result, ",\"durationNanoseconds\":%llu", duration_nanoseconds) < 0) {
        return -1;
    }
    if (strcmp(status, "failed") == 0 &&
        (fputs(",\"failureMessage\":", result) == EOF ||
         utide_write_json_string(result, "Unity assertion failed; see stdout/stderr") != 0)) {
        return -1;
    }
    if (fputs(",\"generatorVersion\":", result) == EOF ||
        utide_write_json_string(result, utide_generator_version) != 0 ||
        fputs(",\"manifestSha256\":", result) == EOF ||
        utide_write_json_string(result, utide_manifest_sha256) != 0 ||
        fputs("}\n", result) == EOF) {
        return -1;
    }
    return fflush(result) == 0 ? 0 : -1;
}

static int utide_parse_options(int argc, char **argv, struct utide_options *options)
{
    int index;
    memset(options, 0, sizeof(*options));
    for (index = 1; index < argc; ++index) {
        const char *argument = argv[index];
        if (strcmp(argument, "--utide-protocol") == 0) {
            if (options->protocol != NULL || ++index >= argc) return UTIDE_EXIT_USAGE;
            options->protocol = argv[index];
        } else if (strcmp(argument, "--utide-mode") == 0) {
            if (options->mode != NULL || ++index >= argc) return UTIDE_EXIT_USAGE;
            options->mode = argv[index];
        } else if (strcmp(argument, "--utide-case") == 0) {
            if (options->case_identity != NULL || ++index >= argc) return UTIDE_EXIT_USAGE;
            options->case_identity = argv[index];
        } else if (strcmp(argument, "--utide-result") == 0) {
            if (options->result_path != NULL || ++index >= argc) return UTIDE_EXIT_USAGE;
            options->result_path = argv[index];
        } else {
            return UTIDE_EXIT_USAGE;
        }
    }
    if (options->protocol == NULL || options->mode == NULL ||
        options->result_path == NULL || options->result_path[0] == '\0') {
        return UTIDE_EXIT_USAGE;
    }
    if (strcmp(options->protocol, utide_protocol) != 0) {
        return UTIDE_EXIT_PROTOCOL;
    }
    if (strcmp(options->mode, "list") == 0) {
        return options->case_identity == NULL ? 0 : UTIDE_EXIT_USAGE;
    }
    if (strcmp(options->mode, "run") == 0) {
        return options->case_identity != NULL && options->case_identity[0] != '\0'
            ? 0 : UTIDE_EXIT_USAGE;
    }
    return UTIDE_EXIT_USAGE;
}

static int utide_list_cases(FILE *result)
{
    size_t index;
    for (index = 0U; index < utide_case_count; ++index) {
        if (utide_write_case_record(result, &utide_cases[index]) != 0) {
            return UTIDE_EXIT_RESULT_WRITE;
        }
    }
    return 0;
}

static const struct utide_case *utide_find_case(const struct utide_options options)
{
    size_t index;
    for (index = 0U; index < utide_case_count; ++index) {
        if (strcmp(options.case_identity, utide_cases[index].identity) == 0) {
            return &utide_cases[index];
        }
    }
    return NULL;
}

int main(int argc, char **argv)
{
    struct utide_options options;
    const struct utide_case *selected;
    FILE *result;
    int status = utide_parse_options(argc, argv, &options);
    if (status != 0) {
        return status;
    }

    result = fopen(options.result_path, "wb");
    if (result == NULL) {
        return UTIDE_EXIT_RESULT_OPEN;
    }
    if (strcmp(options.mode, "list") == 0) {
        status = utide_list_cases(result);
        if (fclose(result) != 0 && status == 0) {
            status = UTIDE_EXIT_RESULT_WRITE;
        }
        return status;
    }

    selected = utide_find_case(options);
    if (selected == NULL) {
        (void)fclose(result);
        return UTIDE_EXIT_CASE_NOT_FOUND;
    }

    {
        UNITY_COUNTER_TYPE failures_before;
        UNITY_COUNTER_TYPE ignores_before;
        clock_t started;
        clock_t finished;
        unsigned long long duration_nanoseconds = 0ULL;
        const char *test_status;
        int unity_status;

        UnityBegin(selected->source_path);
        failures_before = Unity.TestFailures;
        ignores_before = Unity.TestIgnores;
        started = clock();
        UnityDefaultTestRun(
            selected->function,
            selected->identity,
            (UNITY_LINE_TYPE)selected->source_line);
        finished = clock();
        unity_status = UnityEnd();

        if (started != (clock_t)-1 && finished != (clock_t)-1 && finished >= started) {
            duration_nanoseconds =
                ((unsigned long long)(finished - started) * 1000000000ULL) /
                (unsigned long long)CLOCKS_PER_SEC;
        }
        if (Unity.TestFailures > failures_before || unity_status != 0) {
            test_status = "failed";
        } else if (Unity.TestIgnores > ignores_before) {
            test_status = "skipped";
        } else {
            test_status = "passed";
        }

        status = utide_write_result_record(
            result, selected, test_status, duration_nanoseconds);
        if (fclose(result) != 0 || status != 0) {
            return UTIDE_EXIT_RESULT_WRITE;
        }
        return strcmp(test_status, "failed") == 0 ? 1 : 0;
    }
}
`

func renderRunner(manifest Manifest) ([]byte, error) {
	var builder strings.Builder
	builder.Grow(16*1024 + len(manifest.Cases)*256)
	builder.WriteString("/* AUTOGENERATED FILE. DO NOT EDIT.\n")
	builder.WriteString(" * Unit Test IDE deterministic Unity runner.\n")
	fmt.Fprintf(&builder, " * Generator version: %s\n", manifest.GeneratorVersion)
	fmt.Fprintf(&builder, " * Manifest SHA-256: %s\n", manifest.SHA256)
	builder.WriteString(" */\n")
	builder.WriteString(runnerTypesTemplate)
	fmt.Fprintf(&builder, "\nstatic const char utide_protocol[] = %s;\n", cStringLiteral("utide.runner.v1"))
	fmt.Fprintf(&builder, "static const char utide_generator_version[] = %s;\n", cStringLiteral(manifest.GeneratorVersion))
	fmt.Fprintf(&builder, "static const char utide_manifest_sha256[] = %s;\n", cStringLiteral(manifest.SHA256))

	if manifest.SetUp == nil {
		builder.WriteString("\nvoid setUp(void)\n{\n}\n")
	} else {
		builder.WriteString("\nextern void setUp(void);\n")
	}
	if manifest.TearDown == nil {
		builder.WriteString("\nvoid tearDown(void)\n{\n}\n")
	} else {
		builder.WriteString("\nextern void tearDown(void);\n")
	}

	functionCases := make(map[string]TestCase)
	functionNames := make([]string, 0)
	for _, testCase := range manifest.Cases {
		if _, exists := functionCases[testCase.Name]; exists {
			continue
		}
		functionCases[testCase.Name] = testCase
		functionNames = append(functionNames, testCase.Name)
	}
	sort.Strings(functionNames)
	builder.WriteString("\n/* Test declarations are derived only from the sealed manifest. */\n")
	for _, name := range functionNames {
		testCase := functionCases[name]
		fmt.Fprintf(&builder, "extern void %s(%s);\n", testCase.Name, testCase.Parameters)
	}

	callableNames := make([]string, len(manifest.Cases))
	for index, testCase := range manifest.Cases {
		if len(testCase.Arguments) == 0 {
			callableNames[index] = testCase.Name
			continue
		}
		wrapper := fmt.Sprintf("utide_wrapper_%05d", index)
		callableNames[index] = wrapper
		fmt.Fprintf(&builder, "\nstatic void %s(void)\n{\n    %s(%s);\n}\n",
			wrapper, testCase.Name, strings.Join(testCase.Arguments, ", "))
	}

	builder.WriteString("\nstatic const struct utide_case utide_cases[] = {\n")
	if len(manifest.Cases) == 0 {
		builder.WriteString("    { NULL, NULL, NULL, 0UL, NULL, NULL }\n")
	} else {
		for index, testCase := range manifest.Cases {
			argumentsJSON, err := compactArgumentsJSON(testCase.Arguments)
			if err != nil {
				return nil, fmt.Errorf("%w: encode arguments for %q: %v", ErrInvalidGenerateInput, testCase.Identity, err)
			}
			fmt.Fprintf(
				&builder,
				"    { %s, %s, %s, %dUL, %s, %s },\n",
				cStringLiteral(testCase.Identity),
				cStringLiteral(testCase.Name),
				cStringLiteral(testCase.Location.Path),
				testCase.Location.Line,
				cStringLiteral(argumentsJSON),
				callableNames[index],
			)
		}
	}
	builder.WriteString("};\n")
	if len(manifest.Cases) == 0 {
		builder.WriteString("static const size_t utide_case_count = 0U;\n")
	} else {
		builder.WriteString("static const size_t utide_case_count = sizeof(utide_cases) / sizeof(utide_cases[0]);\n")
	}
	builder.WriteString(runnerRuntimeTemplate)
	return []byte(builder.String()), nil
}

func cStringLiteral(value string) string {
	var builder strings.Builder
	builder.Grow(len(value) + 2)
	builder.WriteByte('"')
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch character {
		case '"':
			builder.WriteString(`\"`)
		case '\\':
			builder.WriteString(`\\`)
		case '\a':
			builder.WriteString(`\a`)
		case '\b':
			builder.WriteString(`\b`)
		case '\f':
			builder.WriteString(`\f`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		case '\v':
			builder.WriteString(`\v`)
		default:
			if character >= 0x20 && character <= 0x7e && character != '?' {
				builder.WriteByte(character)
			} else {
				builder.WriteByte('\\')
				builder.WriteString(strconv.FormatInt(int64(character>>6), 8))
				builder.WriteString(strconv.FormatInt(int64(character>>3&7), 8))
				builder.WriteString(strconv.FormatInt(int64(character&7), 8))
			}
		}
	}
	builder.WriteByte('"')
	return builder.String()
}
