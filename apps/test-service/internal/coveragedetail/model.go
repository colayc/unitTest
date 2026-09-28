// Package coveragedetail builds the report-bound, source-bound detailed coverage index.
package coveragedetail

import "unit-test-ide.local/test-service/internal/coveragedomain"

type Status string

const (
	StatusCurrent    Status = "current"
	StatusStale      Status = "stale"
	StatusIncomplete Status = "incomplete"
)

type DeltaMetric struct{ Covered, Total int64 }
type DeltaSummary struct{ Functions, Lines, Branches DeltaMetric }

type Project struct {
	Summary coveragedomain.Summary
	Delta   DeltaSummary
	Status  Status
	Reasons []string
}

func (value Project) Percent(kind string) (float64, bool) {
	return metricPercent(value.Summary, value.Status, kind)
}

type File struct {
	ID           string
	RelativePath string
	SourceSHA256 string
	Summary      coveragedomain.Summary
	Delta        DeltaSummary
	Status       Status
	Reasons      []string
	Functions    []Function
	Lines        []Line
}

func (value File) Percent(kind string) (float64, bool) {
	return metricPercent(value.Summary, value.Status, kind)
}

type Function struct {
	ID              string
	Name            string
	LinkageName     string
	SignatureDigest string
	Start, End      coveragedomain.SourceLocation
	Summary         coveragedomain.Summary
	Delta           DeltaSummary
	Status          Status
	Reasons         []string
	Lines           []Line
	Branches        []Branch
}

func (value Function) Percent(kind string) (float64, bool) {
	return metricPercent(value.Summary, value.Status, kind)
}

type Line struct{ Line, Count int64 }
type Branch struct{ Line, Column, Ordinal, Count int64 }

type Gap struct {
	ID         string
	FileID     string
	FunctionID string
	Kind       string
	Location   coveragedomain.SourceLocation
	Ordinal    int64
}

type Index struct {
	WorkspaceGeneration string
	ProjectID           string
	ReportID            string
	RunID               string
	Toolchain           coveragedomain.ToolchainSnapshot
	Project             Project
	Files               []File
	Gaps                []Gap
}

type BuildInput struct {
	WorkspaceGeneration string
	ProjectID           string
	Report              coveragedomain.Report
	Baseline            *Index
	Functions           []coveragedomain.FunctionObservation
	Sources             []coveragedomain.SourceSnapshot
}

// Detail queries are internal store contracts. HTTP representations are kept
// separate so existing v1 coverage artifacts remain unchanged.
type FileQuery struct {
	ReportID, WorkspaceGeneration, Filter, Sort, Cursor string
	Limit                                               int
}
type FunctionQuery struct {
	ReportID, WorkspaceGeneration, FileID, Filter, Sort, Cursor string
	Limit                                                       int
}
type LineQuery struct {
	ReportID, WorkspaceGeneration, FileID, FunctionID, Filter, Sort, Cursor string
	Limit                                                                   int
}
type FilePage struct {
	Items      []File
	NextCursor string
}
type FunctionPage struct {
	Items      []Function
	NextCursor string
}
type LinePage struct {
	Items      []Line
	NextCursor string
}

func metricPercent(summary coveragedomain.Summary, status Status, kind string) (float64, bool) {
	if status != StatusCurrent {
		return 0, false
	}
	var metric coveragedomain.Metric
	switch kind {
	case "functions":
		metric = summary.Functions
	case "lines":
		metric = summary.Lines
	case "branches":
		metric = summary.Branches
	default:
		return 0, false
	}
	if metric.Total == 0 {
		return 0, false
	}
	return float64(metric.Covered) * 100 / float64(metric.Total), true
}
