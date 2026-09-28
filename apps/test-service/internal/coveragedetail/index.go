package coveragedetail

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/coveragedomain"
)

type lineKey struct{ line int64 }
type branchKey struct{ line, column, ordinal int64 }
type functionAccum struct {
	value         Function
	bestQualified string
	lines         map[lineKey]int64
	branches      map[branchKey]int64
	incomplete    bool
}

// Build constructs a canonical detail index from already source-bound observations.
// Raw collector paths are rejected; callers must first use the normalizer's
// detail-binding path to obtain verified source-snapshot URIs.
func Build(input BuildInput) (Index, error) {
	report, err := coveragedomain.NewReport(input.Report)
	if err != nil {
		return Index{}, fmt.Errorf("%w: report: %v", ErrInvalidDetail, err)
	}
	if !validHex(input.WorkspaceGeneration, 64) || !validProjectID(input.ProjectID) {
		return Index{}, ErrInvalidDetail
	}
	if len(input.Sources) != len(report.Sources) {
		return Index{}, fmt.Errorf("%w: source snapshot set", ErrInvalidDetail)
	}
	sourceByPath := make(map[string]coveragedomain.SourceSnapshot, len(input.Sources))
	reportByPath := make(map[string]coveragedomain.SourceSnapshot, len(report.Sources))
	for _, source := range report.Sources {
		path, err := canonicalPath(source.URI)
		if err != nil {
			return Index{}, err
		}
		if _, ok := reportByPath[path]; ok {
			return Index{}, ErrInvalidDetail
		}
		reportByPath[path] = source
	}
	for _, source := range input.Sources {
		path, err := canonicalPath(source.URI)
		if err != nil || !validHex(source.SHA256, 64) {
			return Index{}, ErrInvalidDetail
		}
		if _, ok := sourceByPath[path]; ok {
			return Index{}, ErrInvalidDetail
		}
		sourceByPath[path] = source
	}
	for path, source := range reportByPath {
		actual, ok := sourceByPath[path]
		if !ok || source.SHA256 != actual.SHA256 {
			return Index{}, fmt.Errorf("%w: stale source snapshot", ErrInvalidDetail)
		}
	}
	index := Index{WorkspaceGeneration: input.WorkspaceGeneration, ProjectID: input.ProjectID, ReportID: report.ID, RunID: report.RunID, Toolchain: report.Toolchain, Project: Project{Status: StatusCurrent}, Files: make([]File, 0, len(sourceByPath))}
	if report.Completeness.Outcome == coveragedomain.OutcomePartial {
		index.Project.Status = StatusIncomplete
		for _, reason := range report.Completeness.Reasons {
			index.Project.Reasons = append(index.Project.Reasons, string(reason))
		}
	}
	fileByPath := make(map[string]*File, len(sourceByPath))
	for _, source := range input.Sources {
		id, err := StableFileID(input.ProjectID, source.URI)
		if err != nil {
			return Index{}, err
		}
		index.Files = append(index.Files, File{ID: id, RelativePath: source.URI, SourceSHA256: source.SHA256, Status: StatusCurrent})
	}
	sort.Slice(index.Files, func(i, j int) bool { return index.Files[i].RelativePath < index.Files[j].RelativePath })
	for i := range index.Files {
		key, _ := canonicalPath(index.Files[i].RelativePath)
		fileByPath[key] = &index.Files[i]
	}
	functions := make(map[string]map[string]*functionAccum, len(index.Files))
	for _, observation := range input.Functions {
		var err error
		observation, err = canonicalObservation(observation)
		if err != nil {
			return Index{}, err
		}
		if !validNavigationRange(observation.Start, observation.End) {
			return Index{}, ErrInvalidDetail
		}
		if observation.IncompleteReason != "" && observation.IncompleteReason != coveragedomain.ObservationIncompleteAttributionAmbiguous && observation.IncompleteReason != coveragedomain.ObservationIncompleteLimit {
			return Index{}, ErrInvalidDetail
		}
		path, err := canonicalPath(observation.File)
		if err != nil {
			return Index{}, fmt.Errorf("%w: unbound observation path", ErrInvalidDetail)
		}
		file := fileByPath[path]
		if file == nil {
			return Index{}, fmt.Errorf("%w: observation has no source snapshot", ErrInvalidDetail)
		}
		key := semanticKey(observation)
		id, err := StableFunctionID(file.ID, key)
		if err != nil {
			return Index{}, fmt.Errorf("%w: function semantic key", ErrInvalidDetail)
		}
		if functions[path] == nil {
			functions[path] = make(map[string]*functionAccum)
		}
		entry := functions[path][id]
		if entry == nil {
			name := observation.QualifiedName
			if name == "" {
				name = observation.LinkageName
			}
			entry = &functionAccum{value: Function{ID: id, Name: name, LinkageName: observation.LinkageName, SignatureDigest: observation.SignatureDigest, Start: observation.Start, End: observation.End, Status: StatusCurrent}, bestQualified: observation.QualifiedName, lines: map[lineKey]int64{}, branches: map[branchKey]int64{}}
			functions[path][id] = entry
		}
		if name := observation.QualifiedName; name != "" && (entry.bestQualified == "" || name < entry.bestQualified) {
			entry.bestQualified = name
			entry.value.Name = name
		}
		if rangeLess(observation.Start, observation.End, entry.value.Start, entry.value.End) {
			entry.value.Start, entry.value.End = observation.Start, observation.End
		}
		if observation.IncompleteReason != "" {
			entry.incomplete = true
			entry.value.Reasons = appendUnique(entry.value.Reasons, string(observation.IncompleteReason))
		}
		if observation.ExecutionCount < 0 || observation.ExecutionCount > coveragedomain.MaxSafeInteger {
			return Index{}, ErrInvalidDetail
		}
		if observation.ExecutionCount > 0 {
			entry.value.Summary.Functions.Covered = 1
		}
		for _, line := range observation.Lines {
			if line.Line < 1 || line.Line > coveragedomain.MaxSafeInteger || line.Count < 0 || line.Count > coveragedomain.MaxSafeInteger {
				return Index{}, ErrInvalidDetail
			}
			key := lineKey{line.Line}
			if line.Count > entry.lines[key] {
				entry.lines[key] = line.Count
			} else if _, ok := entry.lines[key]; !ok {
				entry.lines[key] = 0
			}
		}
		for _, branch := range observation.Branches {
			if branch.Line < 1 || branch.Line > coveragedomain.MaxSafeInteger || branch.Column < 0 || branch.Column > coveragedomain.MaxSafeInteger || branch.Count < 0 || branch.Count > coveragedomain.MaxSafeInteger || branch.Ordinal < 0 || branch.Ordinal > coveragedomain.MaxSafeInteger {
				return Index{}, ErrInvalidDetail
			}
			if !branch.HasOrdinal {
				entry.incomplete = true
				entry.value.Reasons = appendUnique(entry.value.Reasons, "attribution_ambiguous")
				continue
			}
			key := branchKey{branch.Line, branch.Column, branch.Ordinal}
			if branch.Count > entry.branches[key] {
				entry.branches[key] = branch.Count
			} else if _, ok := entry.branches[key]; !ok {
				entry.branches[key] = 0
			}
		}
	}
	for i := range index.Files {
		file := &index.Files[i]
		path, _ := canonicalPath(file.RelativePath)
		lineOwner := make(map[lineKey]*functionAccum)
		branchOwner := make(map[branchKey]*functionAccum)
		for _, entry := range functions[path] {
			for key := range entry.lines {
				if owner := lineOwner[key]; owner != nil && owner != entry {
					markAmbiguous(owner)
					markAmbiguous(entry)
				} else {
					lineOwner[key] = entry
				}
			}
			for key := range entry.branches {
				if owner := branchOwner[key]; owner != nil && owner != entry {
					markAmbiguous(owner)
					markAmbiguous(entry)
				} else {
					branchOwner[key] = entry
				}
			}
		}
		fileLines := map[lineKey]int64{}
		fileBranches := map[branchKey]int64{}
		for _, entry := range functions[path] {
			fn := entry.value
			if entry.incomplete {
				fn.Status = StatusIncomplete
				fn.Summary = coveragedomain.Summary{}
				fn.Lines = nil
				fn.Branches = nil
				file.Status = StatusIncomplete
				for _, reason := range fn.Reasons {
					file.Reasons = appendUnique(file.Reasons, reason)
				}
			} else {
				fn.Summary.Functions.Total = 1
				for key, count := range entry.lines {
					fn.Lines = append(fn.Lines, Line{Line: key.line, Count: count})
					if count > fileLines[key] {
						fileLines[key] = count
					} else if _, ok := fileLines[key]; !ok {
						fileLines[key] = 0
					}
				}
				for key, count := range entry.branches {
					fn.Branches = append(fn.Branches, Branch{Line: key.line, Column: key.column, Ordinal: key.ordinal, Count: count})
					if count > fileBranches[key] {
						fileBranches[key] = count
					} else if _, ok := fileBranches[key]; !ok {
						fileBranches[key] = 0
					}
				}
				sort.Slice(fn.Lines, func(i, j int) bool { return fn.Lines[i].Line < fn.Lines[j].Line })
				sort.Slice(fn.Branches, func(i, j int) bool { return branchLess(fn.Branches[i], fn.Branches[j]) })
				for _, line := range fn.Lines {
					fn.Summary.Lines.Total++
					if line.Count > 0 {
						fn.Summary.Lines.Covered++
					}
				}
				for _, branch := range fn.Branches {
					fn.Summary.Branches.Total++
					if branch.Count > 0 {
						fn.Summary.Branches.Covered++
					}
				}
				var err error
				file.Summary.Functions, err = addMetric(file.Summary.Functions, fn.Summary.Functions)
				if err != nil {
					return Index{}, err
				}
			}
			sort.Strings(fn.Reasons)
			file.Functions = append(file.Functions, fn)
		}
		sort.Slice(file.Functions, func(i, j int) bool { return file.Functions[i].ID < file.Functions[j].ID })
		for key, count := range fileLines {
			file.Lines = append(file.Lines, Line{Line: key.line, Count: count})
			file.Summary.Lines.Total++
			if count > 0 {
				file.Summary.Lines.Covered++
			}
		}
		sort.Slice(file.Lines, func(i, j int) bool { return file.Lines[i].Line < file.Lines[j].Line })
		for _, count := range fileBranches {
			file.Summary.Branches.Total++
			if count > 0 {
				file.Summary.Branches.Covered++
			}
		}
		sort.Strings(file.Reasons)
		if file.Status == StatusIncomplete {
			index.Project.Status = StatusIncomplete
			for _, reason := range file.Reasons {
				index.Project.Reasons = appendUnique(index.Project.Reasons, reason)
			}
		}
		var err error
		index.Project.Summary, err = coveragedomain.AddSummary(index.Project.Summary, file.Summary)
		if err != nil {
			return Index{}, fmt.Errorf("%w: summary overflow", ErrInvalidDetail)
		}
		for _, fn := range file.Functions {
			if fn.Status == StatusCurrent {
				for _, line := range fn.Lines {
					if line.Count == 0 {
						gap, err := newGap(index.ReportID, file.ID, fn.ID, "line", coveragedomain.SourceLocation{Line: line.Line}, 0)
						if err != nil {
							return Index{}, err
						}
						index.Gaps = append(index.Gaps, gap)
					}
				}
				for _, branch := range fn.Branches {
					if branch.Count == 0 {
						gap, err := newGap(index.ReportID, file.ID, fn.ID, "branch", coveragedomain.SourceLocation{Line: branch.Line, Column: branch.Column}, branch.Ordinal)
						if err != nil {
							return Index{}, err
						}
						index.Gaps = append(index.Gaps, gap)
					}
				}
			}
		}
	}
	if index.Project.Summary != report.Summary {
		index.Project.Status = StatusIncomplete
		index.Project.Reasons = appendUnique(index.Project.Reasons, "detail_aggregate_mismatch")
		for i := range index.Files {
			index.Files[i].Status = StatusIncomplete
			index.Files[i].Reasons = appendUnique(index.Files[i].Reasons, "detail_aggregate_mismatch")
			sort.Strings(index.Files[i].Reasons)
		}
	}
	if report.Completeness.Outcome == coveragedomain.OutcomePartial {
		for i := range index.Files {
			file := &index.Files[i]
			file.Status = StatusIncomplete
			for _, reason := range report.Completeness.Reasons {
				file.Reasons = appendUnique(file.Reasons, string(reason))
			}
			sort.Strings(file.Reasons)
			for j := range file.Functions {
				file.Functions[j].Status = StatusIncomplete
				for _, reason := range report.Completeness.Reasons {
					file.Functions[j].Reasons = appendUnique(file.Functions[j].Reasons, string(reason))
				}
				sort.Strings(file.Functions[j].Reasons)
			}
		}
	}
	if index.Project.Status != StatusCurrent {
		index.Gaps = nil
	}
	sort.Strings(index.Project.Reasons)
	sort.Slice(index.Gaps, func(i, j int) bool { return index.Gaps[i].ID < index.Gaps[j].ID })
	if input.Baseline != nil {
		if err := applyDelta(&index, *input.Baseline); err != nil {
			return Index{}, err
		}
	}
	return index, nil
}

func semanticKey(value coveragedomain.FunctionObservation) string {
	kind, key := "linkage", value.LinkageName
	if key == "" {
		kind, key = "qualified", value.QualifiedName
	}
	return kind + ":" + strconv.Itoa(len(key)) + ":" + key + ":signature:" + value.SignatureDigest
}

func canonicalObservation(value coveragedomain.FunctionObservation) (coveragedomain.FunctionObservation, error) {
	if value.InstantiationOrdinal < 0 || value.InstantiationOrdinal > coveragedomain.MaxSafeInteger {
		return coveragedomain.FunctionObservation{}, ErrInvalidDetail
	}
	for _, field := range []string{value.QualifiedName, value.LinkageName, value.SignatureDigest} {
		if len(field) > 8192 || !utf8.ValidString(field) || strings.ContainsRune(field, 0) {
			return coveragedomain.FunctionObservation{}, ErrInvalidDetail
		}
	}
	if value.SignatureDigest != "" && !validHex(value.SignatureDigest, 64) {
		return coveragedomain.FunctionObservation{}, ErrInvalidDetail
	}
	value.QualifiedName = strings.Join(strings.Fields(value.QualifiedName), " ")
	value.LinkageName = strings.Join(strings.Fields(value.LinkageName), " ")
	return value, nil
}

func rangeLess(aStart, aEnd, bStart, bEnd coveragedomain.SourceLocation) bool {
	if aStart.Line == 0 {
		return false
	}
	if bStart.Line == 0 {
		return true
	}
	if aStart.Line != bStart.Line {
		return aStart.Line < bStart.Line
	}
	if aStart.Column != bStart.Column {
		return aStart.Column < bStart.Column
	}
	if aEnd.Line != bEnd.Line {
		return aEnd.Line < bEnd.Line
	}
	return aEnd.Column < bEnd.Column
}
func markAmbiguous(entry *functionAccum) {
	entry.incomplete = true
	entry.value.Reasons = appendUnique(entry.value.Reasons, "attribution_ambiguous")
}
func validNavigationRange(start, end coveragedomain.SourceLocation) bool {
	for _, value := range []coveragedomain.SourceLocation{start, end} {
		if value.Line < 0 || value.Line > coveragedomain.MaxSafeInteger || value.Column < 0 || value.Column > coveragedomain.MaxSafeInteger || value.Line == 0 && value.Column != 0 {
			return false
		}
	}
	if start.Line == 0 && end.Line != 0 {
		return false
	}
	if end.Line != 0 && (end.Line < start.Line || end.Line == start.Line && end.Column != 0 && start.Column != 0 && end.Column < start.Column) {
		return false
	}
	return true
}
func branchLess(a, b Branch) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	if a.Column != b.Column {
		return a.Column < b.Column
	}
	return a.Ordinal < b.Ordinal
}
func appendUnique(values []string, value string) []string {
	for _, item := range values {
		if item == value {
			return values
		}
	}
	return append(values, value)
}
func addMetric(a, b coveragedomain.Metric) (coveragedomain.Metric, error) {
	if a.Covered > coveragedomain.MaxSafeInteger-b.Covered || a.Total > coveragedomain.MaxSafeInteger-b.Total {
		return coveragedomain.Metric{}, ErrInvalidDetail
	}
	return coveragedomain.Metric{Covered: a.Covered + b.Covered, Total: a.Total + b.Total}, nil
}
func newGap(reportID, fileID, functionID, kind string, location coveragedomain.SourceLocation, ordinal int64) (Gap, error) {
	id, err := StableGapID(reportID, functionID, kind, location, ordinal)
	if err != nil {
		return Gap{}, err
	}
	return Gap{ID: id, FileID: fileID, FunctionID: functionID, Kind: kind, Location: location, Ordinal: ordinal}, nil
}
