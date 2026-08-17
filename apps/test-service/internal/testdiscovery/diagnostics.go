package testdiscovery

import (
	"fmt"

	"unit-test-ide.local/test-service/internal/testdomain"
)

const (
	DegradedDiscoveryFailed    = "framework_discovery_failed"
	DegradedDiscoveryPartial   = "framework_discovery_partial"
	DegradedDiscoveryMalformed = "framework_discovery_malformed"
	DegradedDuplicateIdentity  = "duplicate_test_identity"
)

func degradationDiagnostic(ctestName, reason string) testdomain.Diagnostic {
	code := "test.discovery.invalid"
	message := fmt.Sprintf("测试容器 %q 的 Framework discovery 输出无效，已降级为 Opaque CTest。", ctestName)
	switch reason {
	case DegradedDiscoveryFailed:
		code = "test.discovery.failed"
		message = fmt.Sprintf("测试容器 %q 的 Framework discovery 失败，已降级为 Opaque CTest。", ctestName)
	case DegradedDiscoveryPartial:
		code = "test.discovery.partial"
		message = fmt.Sprintf("测试容器 %q 的 Framework discovery 结果不完整，已丢弃未提交 case。", ctestName)
	case DegradedDuplicateIdentity:
		code = "test.discovery.duplicate_identity"
		message = fmt.Sprintf("测试容器 %q 返回重复的 case identity，已降级为 Opaque CTest。", ctestName)
	}
	return testdomain.Diagnostic{
		Severity: "warning",
		Category: "framework_output_invalid",
		Code:     code,
		Message:  message,
	}
}
