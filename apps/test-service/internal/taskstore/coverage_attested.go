package taskstore

import (
	"context"
	"database/sql"
	"fmt"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
)

// The attested read is deliberately bounded independently of the public page
// sizes. A truncated graph is never returned as a current index.
const (
	maxAttestedFiles     = 100_000
	maxAttestedFunctions = 200_000
	maxAttestedPoints    = 2_000_000
)

// ReadValidatedCoverageIndex replays the complete persisted 016 graph inside
// one read-only snapshot. It does not attest live source bytes; callers must
// do that before using a returned current ID for generation.
func (s *Store) ReadValidatedCoverageIndex(ctx context.Context, q coveragedetail.CurrentIndexQuery) (coveragedetail.Index, error) {
	if s != nil && (!s.detailAvailable || !s.attestationAvailable || s.attestationInvalid) {
		return coveragedetail.Index{}, task.ErrStorageUnavailable
	}
	if s == nil || ctx == nil || !validProjectID(q.ProjectID) || !lowerHex(q.ReportID, 32) || !lowerHex(q.WorkspaceGeneration, 64) {
		return coveragedetail.Index{}, task.ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return coveragedetail.Index{}, storageError("begin attested CoverageDetail read", err)
	}
	defer tx.Rollback()
	if err := preflightAttestedRowSizes(ctx, tx, q.ReportID); err != nil {
		return coveragedetail.Index{}, err
	}
	report, err := scanCoverageReport(tx.QueryRowContext(ctx, coverageReportSelect+` WHERE report_id=?`, q.ReportID))
	if isNoRows(err) {
		return coveragedetail.Index{}, task.ErrNotFound
	}
	if err != nil {
		return coveragedetail.Index{}, storageError("read attested CoverageReport", err)
	}
	run, err := scanCoverageRun(tx.QueryRowContext(ctx, coverageRunSelect+` WHERE coverage_run_id=?`, report.RunID))
	if err != nil {
		return coveragedetail.Index{}, storageError("read attested CoverageRun", err)
	}
	if _, err := validateCoverageReportForRun(report, run, run.TaskID); err != nil || run.Request.ProjectID != q.ProjectID || run.Request.WorkspaceGeneration != q.WorkspaceGeneration {
		return coveragedetail.Index{}, storageError("validate attested CoverageReport owner", err)
	}
	h, err := readDetailHeader(ctx, tx, report)
	if err != nil {
		return coveragedetail.Index{}, err
	}
	if h.project != q.ProjectID || h.workspace != q.WorkspaceGeneration {
		return coveragedetail.Index{}, task.ErrInvalidArgument
	}
	if len(h.projectValue.Reasons) > 128 {
		return coveragedetail.Index{}, storageError("attested project reasons bound", nil)
	}
	if h.projectValue.Status != coveragedetail.StatusCurrent {
		return coveragedetail.Index{}, coveragedetail.ErrStale
	}
	index := coveragedetail.Index{ProjectID: h.project, ReportID: report.ID, RunID: h.runID, WorkspaceGeneration: h.workspace, Toolchain: report.Toolchain, Project: h.projectValue}
	if err := loadAttestedFiles(ctx, tx, &index); err != nil {
		return coveragedetail.Index{}, err
	}
	if err := loadAttestedFunctions(ctx, tx, &index); err != nil {
		return coveragedetail.Index{}, err
	}
	if err := loadAttestedPoints(ctx, tx, &index); err != nil {
		return coveragedetail.Index{}, err
	}
	if err := loadAttestedGaps(ctx, tx, &index); err != nil {
		return coveragedetail.Index{}, err
	}
	if err := validateCoverageSourceManifest(ctx, tx, index); err != nil {
		return coveragedetail.Index{}, err
	}
	if err := validateDetailIndex(index); err != nil || !validDetailReportSemantics(index, report) {
		return coveragedetail.Index{}, storageError("validate attested CoverageDetail graph", err)
	}
	if err := tx.Commit(); err != nil {
		return coveragedetail.Index{}, storageError("commit attested CoverageDetail read", err)
	}
	return index, nil
}

// Check lengths inside SQLite before scanning attacker-corrupted optional
// detail rows into Go strings or byte slices. The table/column expressions
// are constants; the report ID is always bound as a parameter.
func preflightAttestedRowSizes(ctx context.Context, tx *sql.Tx, reportID string) error {
	checks := []struct{ table, oversize string }{
		{"coverage_detail_reports", `length(CAST(project_id AS BLOB))>64 OR length(CAST(workspace_generation AS BLOB))>64 OR length(CAST(coverage_run_id AS BLOB))>32 OR length(CAST(toolchain_json AS BLOB))>4096 OR length(CAST(project_summary_json AS BLOB))>4096 OR length(CAST(project_delta_json AS BLOB))>4096 OR length(CAST(project_status AS BLOB))>16 OR length(cursor_key)>32`},
		{"coverage_detail_files", `length(CAST(file_id AS BLOB))>32 OR length(CAST(relative_path AS BLOB))>4096 OR length(CAST(source_sha256 AS BLOB))>64 OR length(CAST(summary_json AS BLOB))>4096 OR length(CAST(delta_json AS BLOB))>4096 OR length(CAST(status AS BLOB))>16`},
		{"coverage_detail_functions", `length(CAST(file_id AS BLOB))>32 OR length(CAST(function_id AS BLOB))>32 OR length(CAST(name AS BLOB))>8192 OR length(CAST(linkage_name AS BLOB))>8192 OR length(CAST(signature_digest AS BLOB))>64 OR length(CAST(summary_json AS BLOB))>4096 OR length(CAST(delta_json AS BLOB))>4096 OR length(CAST(status AS BLOB))>16`},
		{"coverage_detail_reasons", `length(CAST(owner_kind AS BLOB))>16 OR length(CAST(owner_id AS BLOB))>32 OR length(CAST(reason AS BLOB))>128`},
		{"coverage_detail_manifests", `length(CAST(project_id AS BLOB))>64 OR length(CAST(workspace_generation AS BLOB))>64 OR length(CAST(manifest_sha256 AS BLOB))>64`},
		{"coverage_detail_manifest_files", `length(CAST(relative_path AS BLOB))>4096 OR length(CAST(source_sha256 AS BLOB))>64`},
	}
	for _, check := range checks {
		var oversized int
		query := `SELECT EXISTS(SELECT 1 FROM ` + check.table + ` WHERE report_id=? AND (` + check.oversize + `))`
		if err := tx.QueryRowContext(ctx, query, reportID).Scan(&oversized); err != nil || oversized != 0 {
			return storageError("attested CoverageDetail row size", err)
		}
	}
	return nil
}

func loadAttestedFiles(ctx context.Context, tx *sql.Tx, index *coveragedetail.Index) error {
	rows, err := tx.QueryContext(ctx, `SELECT file_id,relative_path,source_sha256,summary_json,delta_json,status FROM coverage_detail_files WHERE report_id=? ORDER BY relative_path LIMIT ?`, index.ReportID, maxAttestedFiles+1)
	if err != nil {
		return storageError("read attested files", err)
	}
	for rows.Next() {
		var file coveragedetail.File
		var summary, delta []byte
		var status string
		if err := rows.Scan(&file.ID, &file.RelativePath, &file.SourceSHA256, &summary, &delta, &status); err != nil || decodeDetailCanonical(summary, &file.Summary) != nil || decodeDetailCanonical(delta, &file.Delta) != nil {
			rows.Close()
			return storageError("decode attested file", err)
		}
		file.Status = coveragedetail.Status(status)
		index.Files = append(index.Files, file)
	}
	if err := rows.Err(); err != nil || len(index.Files) > maxAttestedFiles {
		rows.Close()
		return storageError("attested file bound", err)
	}
	rows.Close()
	for i := range index.Files {
		file := &index.Files[i]
		file.Reasons, err = readDetailReasons(ctx, tx, index.ReportID, "file", file.ID)
		if err != nil {
			return err
		}
		if len(file.Reasons) > 128 {
			return storageError("attested file reasons bound", nil)
		}
		want, e := coveragedetail.StableFileID(index.ProjectID, file.RelativePath)
		if e != nil || want != file.ID || !lowerHex(file.SourceSHA256, 64) {
			return storageError("validate attested file identity", e)
		}
	}
	return nil
}

func loadAttestedFunctions(ctx context.Context, tx *sql.Tx, index *coveragedetail.Index) error {
	fileByID := make(map[string]*coveragedetail.File, len(index.Files))
	for i := range index.Files {
		fileByID[index.Files[i].ID] = &index.Files[i]
	}
	rows, err := tx.QueryContext(ctx, `SELECT file_id,function_id,name,linkage_name,signature_digest,start_line,start_column,end_line,end_column,summary_json,delta_json,status FROM coverage_detail_functions WHERE report_id=? ORDER BY function_id LIMIT ?`, index.ReportID, maxAttestedFunctions+1)
	if err != nil {
		return storageError("read attested functions", err)
	}
	count := 0
	for rows.Next() {
		var fileID string
		var fn coveragedetail.Function
		var summary, delta []byte
		var status string
		if err := rows.Scan(&fileID, &fn.ID, &fn.Name, &fn.LinkageName, &fn.SignatureDigest, &fn.Start.Line, &fn.Start.Column, &fn.End.Line, &fn.End.Column, &summary, &delta, &status); err != nil || decodeDetailCanonical(summary, &fn.Summary) != nil || decodeDetailCanonical(delta, &fn.Delta) != nil {
			rows.Close()
			return storageError("decode attested function", err)
		}
		fn.Status = coveragedetail.Status(status)
		file := fileByID[fileID]
		if file == nil || !attestedFunctionIdentity(fileID, fn) {
			rows.Close()
			return storageError("validate attested function identity", nil)
		}
		file.Functions = append(file.Functions, fn)
		count++
	}
	if err := rows.Err(); err != nil || count > maxAttestedFunctions {
		rows.Close()
		return storageError("attested function bound", err)
	}
	rows.Close()
	for i := range index.Files {
		for j := range index.Files[i].Functions {
			fn := &index.Files[i].Functions[j]
			fn.Reasons, err = readDetailReasons(ctx, tx, index.ReportID, "function", fn.ID)
			if err != nil {
				return err
			}
			if len(fn.Reasons) > 128 {
				return storageError("attested function reasons bound", nil)
			}
		}
	}
	return nil
}

func attestedFunctionIdentity(fileID string, fn coveragedetail.Function) bool {
	name := fn.LinkageName
	kind := "linkage"
	if name == "" {
		name = fn.Name
		kind = "qualified"
	}
	if name == "" || len(name) > 8192 || len(fn.Name) > 8192 || !utf8.ValidString(name) || !utf8.ValidString(fn.Name) ||
		fn.Start.Line < 0 || fn.Start.Line > coveragedomain.MaxSafeInteger || fn.End.Line < fn.Start.Line || fn.End.Line > coveragedomain.MaxSafeInteger ||
		fn.Start.Column < 0 || fn.Start.Column > coveragedomain.MaxSafeInteger || fn.End.Column < 0 || fn.End.Column > coveragedomain.MaxSafeInteger ||
		fn.Start.Line == 0 && (fn.Start.Column != 0 || fn.End.Line != 0) || fn.End.Line == 0 && fn.End.Column != 0 ||
		fn.End.Line == fn.Start.Line && fn.End.Column != 0 && fn.Start.Column != 0 && fn.End.Column < fn.Start.Column ||
		fn.SignatureDigest != "" && !lowerHex(fn.SignatureDigest, 64) {
		return false
	}
	semantic := fmt.Sprintf("%s:%d:%s:signature:%s", kind, len(name), name, fn.SignatureDigest)
	want, err := coveragedetail.StableFunctionID(fileID, semantic)
	return err == nil && want == fn.ID
}

func loadAttestedPoints(ctx context.Context, tx *sql.Tx, index *coveragedetail.Index) error {
	files := make(map[string]*coveragedetail.File, len(index.Files))
	functions := map[string]*coveragedetail.Function{}
	for i := range index.Files {
		file := &index.Files[i]
		files[file.ID] = file
		for j := range file.Functions {
			functions[file.Functions[j].ID] = &file.Functions[j]
		}
	}
	if err := readAttestedLines(ctx, tx, `SELECT file_id,line,count FROM coverage_detail_file_lines WHERE report_id=? ORDER BY file_id,line LIMIT ?`, index.ReportID, func(id string, line coveragedetail.Line) bool {
		if files[id] == nil {
			return false
		}
		files[id].Lines = append(files[id].Lines, line)
		return true
	}); err != nil {
		return err
	}
	if err := readAttestedLines(ctx, tx, `SELECT function_id,line,count FROM coverage_detail_function_lines WHERE report_id=? ORDER BY function_id,line LIMIT ?`, index.ReportID, func(id string, line coveragedetail.Line) bool {
		if functions[id] == nil {
			return false
		}
		functions[id].Lines = append(functions[id].Lines, line)
		return true
	}); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT function_id,line,column,ordinal,count FROM coverage_detail_branches WHERE report_id=? ORDER BY function_id,line,column,ordinal LIMIT ?`, index.ReportID, maxAttestedPoints+1)
	if err != nil {
		return storageError("read attested branches", err)
	}
	count := 0
	for rows.Next() {
		var id string
		var branch coveragedetail.Branch
		if err := rows.Scan(&id, &branch.Line, &branch.Column, &branch.Ordinal, &branch.Count); err != nil || functions[id] == nil {
			rows.Close()
			return storageError("decode attested branch", err)
		}
		functions[id].Branches = append(functions[id].Branches, branch)
		count++
	}
	if err := rows.Err(); err != nil || count > maxAttestedPoints {
		rows.Close()
		return storageError("attested branch bound", err)
	}
	return rows.Close()
}

func readAttestedLines(ctx context.Context, tx *sql.Tx, query, reportID string, add func(string, coveragedetail.Line) bool) error {
	rows, err := tx.QueryContext(ctx, query, reportID, maxAttestedPoints+1)
	if err != nil {
		return storageError("read attested lines", err)
	}
	count := 0
	for rows.Next() {
		var id string
		var line coveragedetail.Line
		if err := rows.Scan(&id, &line.Line, &line.Count); err != nil || !validDetailLine(line) || !add(id, line) {
			rows.Close()
			return storageError("decode attested line", err)
		}
		count++
	}
	if err := rows.Err(); err != nil || count > maxAttestedPoints {
		rows.Close()
		return storageError("attested line bound", err)
	}
	return rows.Close()
}

func loadAttestedGaps(ctx context.Context, tx *sql.Tx, index *coveragedetail.Index) error {
	rows, err := tx.QueryContext(ctx, `SELECT gap_id,file_id,function_id,kind,line,column,ordinal FROM coverage_detail_gaps WHERE report_id=? ORDER BY gap_id LIMIT ?`, index.ReportID, maxAttestedPoints+1)
	if err != nil {
		return storageError("read attested gaps", err)
	}
	for rows.Next() {
		var gap coveragedetail.Gap
		if err := rows.Scan(&gap.ID, &gap.FileID, &gap.FunctionID, &gap.Kind, &gap.Location.Line, &gap.Location.Column, &gap.Ordinal); err != nil {
			rows.Close()
			return storageError("decode attested gap", err)
		}
		index.Gaps = append(index.Gaps, gap)
	}
	if err := rows.Err(); err != nil || len(index.Gaps) > maxAttestedPoints {
		rows.Close()
		return storageError("attested gap bound", err)
	}
	return rows.Close()
}
