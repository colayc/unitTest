package taskstore

import (
	"context"
	"fmt"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
)

type currentBranchKey struct{ line, column, ordinal int64 }

// currentBranchArms validates ownership within one file, before a page can
// discard sibling functions. The same source coordinate in another file is
// independent. Numeric bounds and summary unions are already attested.
func currentBranchArms(functions []coveragedetail.Function) (map[currentBranchKey]int64, error) {
	arms := make(map[currentBranchKey]int64)
	owners := make(map[currentBranchKey]string)
	for _, fn := range functions {
		if fn.Status != coveragedetail.StatusCurrent {
			return nil, storageError("current line function is incomplete", nil)
		}
		attributed := make(map[int64]bool, len(fn.Lines))
		for _, line := range fn.Lines {
			attributed[line.Line] = true
		}
		for _, branch := range fn.Branches {
			key := currentBranchKey{branch.Line, branch.Column, branch.Ordinal}
			if !attributed[branch.Line] {
				return nil, storageError("branch has no attributed line", nil)
			}
			if owner, exists := owners[key]; exists {
				if owner != fn.ID || arms[key] != branch.Count {
					return nil, storageError("conflicting current branch owner", nil)
				}
				continue
			}
			owners[key], arms[key] = fn.ID, branch.Count
		}
	}
	return arms, nil
}

// projectCurrentLines accepts only an attested current graph. Raw observations
// remain unchanged; the page carries branch evidence from its selected owner.
func projectCurrentLines(index coveragedetail.Index, fileID, functionID string) (map[int64]coveragedetail.LineDetail, error) {
	if index.Project.Status != coveragedetail.StatusCurrent {
		return nil, coveragedetail.ErrStale
	}
	var file *coveragedetail.File
	var arms map[currentBranchKey]int64
	for i := range index.Files {
		// Attestation's summary unions cannot prove branch attribution or
		// exclusive ownership. Check every loaded file before narrowing.
		fileArms, err := currentBranchArms(index.Files[i].Functions)
		if err != nil {
			return nil, err
		}
		if index.Files[i].ID == fileID {
			if file != nil {
				return nil, storageError("duplicate current line file", nil)
			}
			file = &index.Files[i]
			arms = fileArms
		}
	}
	if file == nil {
		return nil, task.ErrNotFound
	}
	if file.Status != coveragedetail.StatusCurrent {
		return nil, storageError("current line file is incomplete", nil)
	}
	lines, summary := file.Lines, file.Summary
	if functionID != "" {
		var fn *coveragedetail.Function
		for i := range file.Functions {
			if file.Functions[i].ID == functionID {
				if fn != nil {
					return nil, storageError("duplicate current line function", nil)
				}
				fn = &file.Functions[i]
			}
		}
		if fn == nil {
			return nil, task.ErrNotFound
		}
		lines, summary = fn.Lines, fn.Summary
		// The whole graph has passed ownership/attribution checks; only the
		// selected function's arms contribute to its current page.
		arms = make(map[currentBranchKey]int64, len(fn.Branches))
		for _, branch := range fn.Branches {
			arms[currentBranchKey{branch.Line, branch.Column, branch.Ordinal}] = branch.Count
		}
	}
	projected := make(map[int64]coveragedetail.LineDetail, len(lines))
	for _, line := range lines {
		if _, duplicate := projected[line.Line]; duplicate || !validDetailLine(line) {
			return nil, storageError("invalid current line projection", nil)
		}
		projected[line.Line] = coveragedetail.LineDetail{Line: line.Line, Count: line.Count}
	}
	for key, count := range arms {
		line, exists := projected[key.line]
		if !exists {
			return nil, storageError("branch has no attributed line", nil)
		}
		line.BranchesTotal++
		if count > 0 {
			line.BranchesCovered++
		}
		projected[key.line] = line
	}
	var lineMetric, branchMetric coveragedomain.Metric
	for _, line := range projected {
		lineMetric.Total++
		if line.Count > 0 {
			lineMetric.Covered++
		}
		branchMetric.Covered += line.BranchesCovered
		branchMetric.Total += line.BranchesTotal
	}
	if lineMetric != summary.Lines || branchMetric != summary.Branches {
		return nil, storageError("current line projection summary mismatch", nil)
	}
	return projected, nil
}

func (s *Store) ListCoverageLines(ctx context.Context, q coveragedetail.LineQuery) (coveragedetail.LinePage, error) {
	if s != nil && (!s.detailAvailable || !s.attestationAvailable || s.attestationInvalid) {
		return coveragedetail.LinePage{}, task.ErrStorageUnavailable
	}
	if s == nil || ctx == nil || !validDetailQuery(q.ReportID, q.WorkspaceGeneration, q.Sort, q.Limit, detailLineLimit) || (q.FileID == "" && q.FunctionID == "") || (q.FileID != "" && !lowerHex(q.FileID, 32)) || (q.FunctionID != "" && !lowerHex(q.FunctionID, 32)) || (q.Filter != "" && q.Filter != "covered" && q.Filter != "uncovered") {
		return coveragedetail.LinePage{}, task.ErrInvalidArgument
	}
	tx, h, err := s.detailRead(ctx, q.ReportID, q.WorkspaceGeneration)
	if err != nil {
		return coveragedetail.LinePage{}, err
	}
	defer tx.Rollback()
	index, err := readValidatedCoverageIndexSnapshot(ctx, tx, coveragedetail.CurrentIndexQuery{
		ProjectID: h.project, ReportID: q.ReportID, WorkspaceGeneration: q.WorkspaceGeneration,
	})
	if err != nil {
		return coveragedetail.LinePage{}, err
	}
	if q.FileID == "" {
		if err := tx.QueryRowContext(ctx, `SELECT file_id FROM coverage_detail_functions WHERE report_id=? AND function_id=?`, q.ReportID, q.FunctionID).Scan(&q.FileID); isNoRows(err) {
			return coveragedetail.LinePage{}, task.ErrNotFound
		} else if err != nil {
			return coveragedetail.LinePage{}, storageError("resolve CoverageDetail function", err)
		}
	}
	projected, err := projectCurrentLines(index, q.FileID, q.FunctionID)
	if err != nil {
		return coveragedetail.LinePage{}, err
	}
	scope := detailScope("lines", q.ReportID, q.WorkspaceGeneration, q.FileID+":"+q.FunctionID, q.Filter, q.Sort, q.Limit)
	last := ""
	if q.Cursor != "" {
		last, err = decodeDetailCursor(h.key, scope, q.Cursor)
		if err != nil {
			return coveragedetail.LinePage{}, err
		}
	}
	table, parentColumn, parentID := "coverage_detail_file_lines", "file_id", q.FileID
	if q.FunctionID != "" {
		table, parentColumn, parentID = "coverage_detail_function_lines", "function_id", q.FunctionID
	}
	query := `SELECT line,count FROM ` + table + ` WHERE report_id=? AND ` + parentColumn + `=?`
	args := []any{q.ReportID, parentID}
	if q.Filter == "covered" {
		query += ` AND count>0`
	} else if q.Filter == "uncovered" {
		query += ` AND count=0`
	}
	if last != "" {
		var number int64
		if _, err := fmt.Sscanf(last, "%d", &number); err != nil || number < 1 || fmt.Sprint(number) != last {
			return coveragedetail.LinePage{}, task.ErrInvalidArgument
		}
		op := ">"
		if q.Sort == "desc" {
			op = "<"
		}
		query += ` AND line ` + op + ` ?`
		args = append(args, number)
	}
	query += ` ORDER BY line ` + detailOrder(q.Sort) + ` LIMIT ?`
	args = append(args, q.Limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return coveragedetail.LinePage{}, storageError("list CoverageDetail lines", err)
	}
	items := []coveragedetail.LineDetail{}
	for rows.Next() {
		var line coveragedetail.Line
		if err := rows.Scan(&line.Line, &line.Count); err != nil || !validDetailLine(line) {
			rows.Close()
			return coveragedetail.LinePage{}, storageError("scan CoverageDetail line", err)
		}
		item, exists := projected[line.Line]
		if !exists || item.Count != line.Count {
			rows.Close()
			return coveragedetail.LinePage{}, storageError("CoverageDetail line snapshot mismatch", nil)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return coveragedetail.LinePage{}, storageError("list CoverageDetail lines", err)
	}
	rows.Close()
	page := coveragedetail.LinePage{Items: items}
	if len(items) > q.Limit {
		page.Items = items[:q.Limit]
		page.NextCursor = encodeDetailCursor(h.key, scope, fmt.Sprint(page.Items[len(page.Items)-1].Line))
	}
	if err := tx.Commit(); err != nil {
		return coveragedetail.LinePage{}, storageError("commit CoverageDetail read", err)
	}
	return page, nil
}
