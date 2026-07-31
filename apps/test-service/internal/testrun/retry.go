package testrun

import (
	"sort"

	"unit-test-ide.local/test-service/internal/testdomain"
)

type retryScope int

const (
	retryNone retryScope = iota
	retryItem
	retryContainer
)

func resolveFailedRun(
	catalog testdomain.Catalog,
	sourceRunID string,
	previous testdomain.TestRun,
	limits testdomain.Limits,
) (testdomain.SelectionSnapshot, error) {
	if previous.RunID != sourceRunID ||
		previous.Status != testdomain.RunCompleted {
		return testdomain.SelectionSnapshot{}, testdomain.ErrInvalidSelection
	}
	if previous.ProjectID != catalog.ProjectID ||
		previous.ProfileID != catalog.ProfileID {
		return testdomain.SelectionSnapshot{}, testdomain.ErrCatalogStale
	}
	if limits.MaxSelectionSize < 1 || limits.MaxSelectionSize > 100_000 {
		return testdomain.SelectionSnapshot{}, testdomain.ErrInvalidSelection
	}

	containers := make(map[testdomain.ID]testdomain.Container, len(catalog.Containers))
	for _, container := range catalog.Containers {
		containers[container.ID] = container
	}
	items := make(map[testdomain.ID]testdomain.Item, len(catalog.Items))
	for _, item := range catalog.Items {
		items[item.ID] = item
	}
	selectedContainers := make(map[testdomain.ID]struct{})
	selectedItems := make(map[testdomain.ID]testdomain.ID)
	for _, candidate := range previous.Results {
		result, err := testdomain.NewTestItemResult(candidate)
		if err != nil {
			return testdomain.SelectionSnapshot{}, err
		}
		switch classifyRetry(result) {
		case retryContainer:
			if _, exists := containers[result.ContainerID]; !exists {
				return testdomain.SelectionSnapshot{},
					testdomain.ErrUnknownSelectionID
			}
			selectedContainers[result.ContainerID] = struct{}{}
		case retryItem:
			item, exists := items[result.ItemID]
			if !exists || item.Kind != testdomain.ItemCase ||
				item.ContainerID != result.ContainerID {
				return testdomain.SelectionSnapshot{},
					testdomain.ErrUnknownSelectionID
			}
			if _, exists := containers[item.ContainerID]; !exists {
				return testdomain.SelectionSnapshot{},
					testdomain.ErrUnknownSelectionID
			}
			selectedItems[item.ID] = item.ContainerID
		}
	}
	for itemID, containerID := range selectedItems {
		if _, widened := selectedContainers[containerID]; widened {
			delete(selectedItems, itemID)
		}
	}
	if len(selectedContainers)+len(selectedItems) == 0 {
		return testdomain.SelectionSnapshot{}, testdomain.ErrEmptySelection
	}
	if len(selectedContainers)+len(selectedItems) > limits.MaxSelectionSize {
		return testdomain.SelectionSnapshot{}, testdomain.ErrSelectionTooLarge
	}
	return testdomain.SelectionSnapshot{
		Mode:         testdomain.SelectionFailedFromRun,
		ContainerIDs: sortedRetryContainers(selectedContainers),
		ItemIDs:      sortedRetryItems(selectedItems),
		SourceRunID:  sourceRunID,
	}, nil
}

func classifyRetry(result testdomain.TestItemResult) retryScope {
	switch result.Outcome {
	case testdomain.ItemSkipped, testdomain.ItemCancelled:
		return retryNone
	}
	if result.Partial {
		return retryContainer
	}
	switch result.Outcome {
	case testdomain.ItemFailed:
		if assertionFailure(result.FailureDetails) {
			return retryItem
		}
		return retryContainer
	case testdomain.ItemErrored, testdomain.ItemTimedOut:
		return retryContainer
	case testdomain.ItemNotRun:
		switch result.Reason {
		case testdomain.ReasonBuildBlocked,
			testdomain.ReasonContainerTerminated,
			testdomain.ReasonServiceRestarted,
			testdomain.ReasonStaleCatalog:
			return retryContainer
		default:
			return retryNone
		}
	default:
		return retryNone
	}
}

func assertionFailure(details []testdomain.FailureDetail) bool {
	if len(details) == 0 {
		return false
	}
	for _, detail := range details {
		if detail.Category != "assertion_failure" {
			return false
		}
	}
	return true
}

func sortedRetryContainers(
	values map[testdomain.ID]struct{},
) []testdomain.ID {
	result := make([]testdomain.ID, 0, len(values))
	for id := range values {
		result = append(result, id)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left] < result[right]
	})
	return result
}

func sortedRetryItems(
	values map[testdomain.ID]testdomain.ID,
) []testdomain.ID {
	result := make([]testdomain.ID, 0, len(values))
	for id := range values {
		result = append(result, id)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left] < result[right]
	})
	return result
}
