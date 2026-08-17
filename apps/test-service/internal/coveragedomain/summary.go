package coveragedomain

import "errors"

const MaxSafeInteger int64 = 9_007_199_254_740_991

var ErrInvalidSummary = errors.New("invalid coverage summary")

type Metric struct {
	Covered int64 `json:"covered"`
	Total   int64 `json:"total"`
}

type Summary struct {
	Lines     Metric `json:"lines"`
	Branches  Metric `json:"branches"`
	Functions Metric `json:"functions"`
}

func NewSummary(value Summary) (Summary, error) {
	if !validMetric(value.Lines) || !validMetric(value.Branches) || !validMetric(value.Functions) {
		return Summary{}, ErrInvalidSummary
	}
	return value, nil
}

func AddSummary(left, right Summary) (Summary, error) {
	if _, err := NewSummary(left); err != nil {
		return Summary{}, err
	}
	if _, err := NewSummary(right); err != nil {
		return Summary{}, err
	}
	lines, ok := addMetric(left.Lines, right.Lines)
	if !ok {
		return Summary{}, ErrInvalidSummary
	}
	branches, ok := addMetric(left.Branches, right.Branches)
	if !ok {
		return Summary{}, ErrInvalidSummary
	}
	functions, ok := addMetric(left.Functions, right.Functions)
	if !ok {
		return Summary{}, ErrInvalidSummary
	}
	return Summary{Lines: lines, Branches: branches, Functions: functions}, nil
}

func validMetric(value Metric) bool {
	return value.Covered >= 0 && value.Covered <= value.Total && value.Total <= MaxSafeInteger
}

func addMetric(left, right Metric) (Metric, bool) {
	if left.Covered > MaxSafeInteger-right.Covered || left.Total > MaxSafeInteger-right.Total {
		return Metric{}, false
	}
	return Metric{Covered: left.Covered + right.Covered, Total: left.Total + right.Total}, true
}
