package taskstore

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
)

const detailFileLimit = 200
const detailFunctionLimit = 200
const detailLineLimit = 1000

// CoverageDetailReady reports only optional migration/index storage readiness.
// It does not imply a particular report has an index or current source bytes.
func (s *Store) CoverageDetailReady() bool { return s != nil && s.detailAvailable && !s.detailInvalid }

type CoverageDetailStore interface {
	PutCoverageDetail(context.Context, coveragedetail.Index) error
	GetCoverageProject(context.Context, string) (coveragedetail.Project, error)
	ListCoverageFiles(context.Context, coveragedetail.FileQuery) (coveragedetail.FilePage, error)
	ListCoverageFunctions(context.Context, coveragedetail.FunctionQuery) (coveragedetail.FunctionPage, error)
	ListCoverageLines(context.Context, coveragedetail.LineQuery) (coveragedetail.LinePage, error)
}

var _ CoverageDetailStore = (*Store)(nil)

type detailCursor struct {
	Scope string `json:"scope"`
	Last  string `json:"last"`
	MAC   string `json:"mac"`
}

type detailHeader struct {
	workspace, project, runID string
	key                       []byte
	projectValue              coveragedetail.Project
}

// PutCoverageDetail is intentionally separate from the v1 report transaction:
// detail failure cannot invalidate an already published v1 coverage report.
func (s *Store) PutCoverageDetail(ctx context.Context, index coveragedetail.Index) error {
	if s != nil && !s.detailAvailable {
		return task.ErrStorageUnavailable
	}
	if s == nil || ctx == nil || !lowerHex(index.ReportID, 32) || !lowerHex(index.RunID, 32) ||
		!lowerHex(index.WorkspaceGeneration, 64) || !validProjectID(index.ProjectID) ||
		!validDetailProject(index.Project) {
		return task.ErrInvalidArgument
	}
	report, err := s.GetCoverageReport(ctx, index.ReportID)
	if err != nil {
		return err
	}
	run, err := s.GetCoverageRun(ctx, index.RunID)
	if err != nil {
		return err
	}
	if report.RunID != index.RunID || run.ID != index.RunID ||
		run.Request.WorkspaceGeneration != index.WorkspaceGeneration || run.Request.ProjectID != index.ProjectID ||
		!reflect.DeepEqual(report.Toolchain, index.Toolchain) ||
		(index.Project.Status == coveragedetail.StatusCurrent && (report.Completeness.Outcome != coveragedomain.OutcomeAvailable || index.Project.Summary != report.Summary)) {
		return task.ErrInvalidArgument
	}
	if err := validateDetailIndex(index); err != nil || !validDetailReportSemantics(index, report) {
		return task.ErrInvalidArgument
	}
	_, _, toolchainJSON, err := encodeCoverageReportMetadata(report)
	if err != nil {
		return task.ErrInvalidArgument
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return storageError("generate CoverageDetail cursor key", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError("begin CoverageDetail", err)
	}
	defer tx.Rollback()
	var runID, workspace, project string
	if err := tx.QueryRowContext(ctx, `SELECT r.coverage_run_id,c.workspace_generation,c.project_id FROM coverage_reports r JOIN coverage_runs c ON c.coverage_run_id=r.coverage_run_id WHERE r.report_id=?`, index.ReportID).Scan(&runID, &workspace, &project); err != nil {
		return storageError("validate CoverageDetail report", err)
	}
	if runID != index.RunID || workspace != index.WorkspaceGeneration || project != index.ProjectID {
		return task.ErrInvalidArgument
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO coverage_detail_reports(report_id,workspace_generation,project_id,coverage_run_id,toolchain_json,project_summary_json,project_delta_json,project_status,cursor_key) VALUES(?,?,?,?,?,?,?,?,?)`, index.ReportID, index.WorkspaceGeneration, index.ProjectID, index.RunID, string(toolchainJSON), mustDetailJSON(index.Project.Summary), mustDetailJSON(index.Project.Delta), index.Project.Status, key); err != nil {
		return storageError("insert CoverageDetail report", err)
	}
	if err := insertDetailReasons(ctx, tx, index.ReportID, "project", index.ReportID, index.Project.Reasons); err != nil {
		return err
	}
	for _, file := range index.Files {
		if _, err := tx.ExecContext(ctx, `INSERT INTO coverage_detail_files(report_id,file_id,relative_path,source_sha256,summary_json,delta_json,status) VALUES(?,?,?,?,?,?,?)`, index.ReportID, file.ID, file.RelativePath, file.SourceSHA256, mustDetailJSON(file.Summary), mustDetailJSON(file.Delta), file.Status); err != nil {
			return storageError("insert CoverageDetail file", err)
		}
		if err := insertDetailReasons(ctx, tx, index.ReportID, "file", file.ID, file.Reasons); err != nil {
			return err
		}
		for _, line := range file.Lines {
			if _, err := tx.ExecContext(ctx, `INSERT INTO coverage_detail_file_lines(report_id,file_id,line,count) VALUES(?,?,?,?)`, index.ReportID, file.ID, line.Line, line.Count); err != nil {
				return storageError("insert CoverageDetail file line", err)
			}
		}
		for _, fn := range file.Functions {
			if _, err := tx.ExecContext(ctx, `INSERT INTO coverage_detail_functions(report_id,file_id,function_id,name,linkage_name,signature_digest,start_line,start_column,end_line,end_column,summary_json,delta_json,status) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, index.ReportID, file.ID, fn.ID, fn.Name, fn.LinkageName, fn.SignatureDigest, fn.Start.Line, fn.Start.Column, fn.End.Line, fn.End.Column, mustDetailJSON(fn.Summary), mustDetailJSON(fn.Delta), fn.Status); err != nil {
				return storageError("insert CoverageDetail function", err)
			}
			if err := insertDetailReasons(ctx, tx, index.ReportID, "function", fn.ID, fn.Reasons); err != nil {
				return err
			}
			for _, line := range fn.Lines {
				if _, err := tx.ExecContext(ctx, `INSERT INTO coverage_detail_function_lines(report_id,function_id,line,count) VALUES(?,?,?,?)`, index.ReportID, fn.ID, line.Line, line.Count); err != nil {
					return storageError("insert CoverageDetail function line", err)
				}
			}
			for _, branch := range fn.Branches {
				if _, err := tx.ExecContext(ctx, `INSERT INTO coverage_detail_branches(report_id,function_id,line,column,ordinal,count) VALUES(?,?,?,?,?,?)`, index.ReportID, fn.ID, branch.Line, branch.Column, branch.Ordinal, branch.Count); err != nil {
					return storageError("insert CoverageDetail branch", err)
				}
			}
		}
	}
	for _, gap := range index.Gaps {
		if _, err := tx.ExecContext(ctx, `INSERT INTO coverage_detail_gaps(report_id,gap_id,file_id,function_id,kind,line,column,ordinal) VALUES(?,?,?,?,?,?,?,?)`, index.ReportID, gap.ID, gap.FileID, gap.FunctionID, gap.Kind, gap.Location.Line, gap.Location.Column, gap.Ordinal); err != nil {
			return storageError("insert CoverageDetail gap", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return storageError("commit CoverageDetail", err)
	}
	return nil
}

func validDetailProject(p coveragedetail.Project) bool {
	return validDetailStatus(p.Status) && validDetailSummary(p.Summary) && validDetailDelta(p.Delta)
}
func validDetailStatus(s coveragedetail.Status) bool {
	return s == coveragedetail.StatusCurrent || s == coveragedetail.StatusStale || s == coveragedetail.StatusIncomplete
}
func validDetailSummary(v coveragedomain.Summary) bool {
	_, err := coveragedomain.NewSummary(v)
	return err == nil
}
func validDetailDelta(v coveragedetail.DeltaSummary) bool {
	for _, m := range []coveragedetail.DeltaMetric{v.Functions, v.Lines, v.Branches} {
		if m.Covered < -coveragedomain.MaxSafeInteger || m.Covered > coveragedomain.MaxSafeInteger || m.Total < -coveragedomain.MaxSafeInteger || m.Total > coveragedomain.MaxSafeInteger {
			return false
		}
	}
	return true
}
func validDetailLine(v coveragedetail.Line) bool {
	return v.Line > 0 && v.Line <= coveragedomain.MaxSafeInteger && v.Count >= 0 && v.Count <= coveragedomain.MaxSafeInteger
}
func validateDetailIndex(index coveragedetail.Index) error {
	files, functions, gaps := map[string]bool{}, map[string]string{}, map[string]bool{}
	paths := map[string]bool{}
	expectedGaps := map[string]bool{}
	projectSummary := coveragedomain.Summary{}
	for _, f := range index.Files {
		if index.Project.Status == coveragedetail.StatusCurrent && f.Status != coveragedetail.StatusCurrent {
			return coveragedetail.ErrInvalidDetail
		}
		if !validDetailReasons(f.Status, f.Reasons) || !reasonsInclude(index.Project.Reasons, f.Reasons) {
			return coveragedetail.ErrInvalidDetail
		}
		id, err := coveragedetail.StableFileID(index.ProjectID, f.RelativePath)
		if err != nil || id != f.ID || files[f.ID] || paths[f.RelativePath] || !lowerHex(f.SourceSHA256, 64) || !validDetailStatus(f.Status) || !validDetailSummary(f.Summary) || !validDetailDelta(f.Delta) {
			return coveragedetail.ErrInvalidDetail
		}
		files[f.ID] = true
		paths[f.RelativePath] = true
		lineIDs := map[int64]bool{}
		fileLineCounts := map[int64]int64{}
		fileBranchCounts := map[[3]int64]int64{}
		functionSummary := coveragedomain.Summary{}
		for _, line := range f.Lines {
			if !validDetailLine(line) || lineIDs[line.Line] {
				return coveragedetail.ErrInvalidDetail
			}
			lineIDs[line.Line] = true
		}
		for _, fn := range f.Functions {
			if f.Status == coveragedetail.StatusCurrent && fn.Status != coveragedetail.StatusCurrent || !validDetailReasons(fn.Status, fn.Reasons) || !reasonsInclude(f.Reasons, fn.Reasons) {
				return coveragedetail.ErrInvalidDetail
			}
			if !lowerHex(fn.ID, 32) || functions[fn.ID] != "" || !validDetailStatus(fn.Status) || !validDetailSummary(fn.Summary) || !validDetailDelta(fn.Delta) {
				return coveragedetail.ErrInvalidDetail
			}
			if fn.Status == coveragedetail.StatusCurrent && fn.Summary.Functions.Total != 1 || fn.Summary.Functions.Total > 1 {
				return coveragedetail.ErrInvalidDetail
			}
			functions[fn.ID] = f.ID
			lines := map[int64]bool{}
			lineMetric := coveragedomain.Metric{}
			for _, line := range fn.Lines {
				if !validDetailLine(line) || lines[line.Line] {
					return coveragedetail.ErrInvalidDetail
				}
				lines[line.Line] = true
				lineMetric.Total++
				if line.Count > 0 {
					lineMetric.Covered++
				}
				if line.Count > fileLineCounts[line.Line] {
					fileLineCounts[line.Line] = line.Count
				} else if _, ok := fileLineCounts[line.Line]; !ok {
					fileLineCounts[line.Line] = 0
				}
				if index.Project.Status == coveragedetail.StatusCurrent && line.Count == 0 {
					id, err := coveragedetail.StableGapID(index.ReportID, fn.ID, "line", coveragedomain.SourceLocation{Line: line.Line}, 0)
					if err != nil {
						return coveragedetail.ErrInvalidDetail
					}
					expectedGaps[id] = true
				}
			}
			branches := map[[3]int64]bool{}
			branchMetric := coveragedomain.Metric{}
			for _, b := range fn.Branches {
				k := [3]int64{b.Line, b.Column, b.Ordinal}
				if b.Line < 1 || b.Line > coveragedomain.MaxSafeInteger || b.Column < 0 || b.Column > coveragedomain.MaxSafeInteger || b.Ordinal < 0 || b.Ordinal > coveragedomain.MaxSafeInteger || b.Count < 0 || b.Count > coveragedomain.MaxSafeInteger || branches[k] {
					return coveragedetail.ErrInvalidDetail
				}
				branches[k] = true
				branchMetric.Total++
				if b.Count > 0 {
					branchMetric.Covered++
				}
				if b.Count > fileBranchCounts[k] {
					fileBranchCounts[k] = b.Count
				} else if _, ok := fileBranchCounts[k]; !ok {
					fileBranchCounts[k] = 0
				}
				if index.Project.Status == coveragedetail.StatusCurrent && b.Count == 0 {
					id, err := coveragedetail.StableGapID(index.ReportID, fn.ID, "branch", coveragedomain.SourceLocation{Line: b.Line, Column: b.Column}, b.Ordinal)
					if err != nil {
						return coveragedetail.ErrInvalidDetail
					}
					expectedGaps[id] = true
				}
			}
			if fn.Summary.Lines != lineMetric || fn.Summary.Branches != branchMetric || fn.Summary.Functions.Total == 0 && (len(fn.Lines) > 0 || len(fn.Branches) > 0) {
				return coveragedetail.ErrInvalidDetail
			}
			var err error
			functionSummary, err = coveragedomain.AddSummary(functionSummary, fn.Summary)
			if err != nil {
				return coveragedetail.ErrInvalidDetail
			}
		}
		if len(fileLineCounts) != len(f.Lines) {
			return coveragedetail.ErrInvalidDetail
		}
		fileLines := coveragedomain.Metric{}
		for _, line := range f.Lines {
			if count, ok := fileLineCounts[line.Line]; !ok || count != line.Count {
				return coveragedetail.ErrInvalidDetail
			}
			fileLines.Total++
			if line.Count > 0 {
				fileLines.Covered++
			}
		}
		fileBranches := coveragedomain.Metric{}
		for _, count := range fileBranchCounts {
			fileBranches.Total++
			if count > 0 {
				fileBranches.Covered++
			}
		}
		if f.Summary.Functions != functionSummary.Functions || f.Summary.Lines != fileLines || f.Summary.Branches != fileBranches {
			return coveragedetail.ErrInvalidDetail
		}
		projectSummary, err = coveragedomain.AddSummary(projectSummary, f.Summary)
		if err != nil {
			return coveragedetail.ErrInvalidDetail
		}
	}
	if index.Project.Summary != projectSummary || !validDetailReasons(index.Project.Status, index.Project.Reasons) {
		return coveragedetail.ErrInvalidDetail
	}
	if index.Project.Status != coveragedetail.StatusCurrent && len(index.Gaps) != 0 {
		return coveragedetail.ErrInvalidDetail
	}
	for _, g := range index.Gaps {
		id, err := coveragedetail.StableGapID(index.ReportID, g.FunctionID, g.Kind, g.Location, g.Ordinal)
		if err != nil || id != g.ID || gaps[g.ID] || !files[g.FileID] || functions[g.FunctionID] != g.FileID || !expectedGaps[g.ID] {
			return coveragedetail.ErrInvalidDetail
		}
		gaps[g.ID] = true
	}
	if len(gaps) != len(expectedGaps) {
		return coveragedetail.ErrInvalidDetail
	}
	return nil
}

func validDetailReasons(status coveragedetail.Status, reasons []string) bool {
	if status == coveragedetail.StatusCurrent {
		return len(reasons) == 0
	}
	return len(reasons) > 0
}
func reasonsInclude(parent, child []string) bool {
	for _, reason := range child {
		found := false
		for _, candidate := range parent {
			if reason == candidate {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func validDetailReportSemantics(index coveragedetail.Index, report coveragedomain.Report) bool {
	mismatch := index.Project.Summary != report.Summary
	reportReasons := map[string]bool{}
	if report.Completeness.Outcome == coveragedomain.OutcomePartial {
		for _, reason := range report.Completeness.Reasons {
			reportReasons[string(reason)] = true
		}
	}
	projectReasons := cloneDetailReasonSet(reportReasons)
	if mismatch {
		projectReasons["detail_aggregate_mismatch"] = true
	}
	for _, file := range index.Files {
		fileReasons := cloneDetailReasonSet(reportReasons)
		if mismatch {
			fileReasons["detail_aggregate_mismatch"] = true
		}
		for _, fn := range file.Functions {
			functionReasons := cloneDetailReasonSet(reportReasons)
			observationIncomplete := false
			for _, reason := range fn.Reasons {
				switch reason {
				case string(coveragedomain.ObservationIncompleteAttributionAmbiguous), string(coveragedomain.ObservationIncompleteLimit):
					functionReasons[reason] = true
					observationIncomplete = true
				default:
					if !reportReasons[reason] {
						return false
					}
				}
			}
			if observationIncomplete && (fn.Summary != (coveragedomain.Summary{}) || len(fn.Lines) != 0 || len(fn.Branches) != 0) {
				return false
			}
			if fn.Status != detailStatusForReasons(functionReasons) || !sameDetailReasons(fn.Reasons, functionReasons) {
				return false
			}
			for reason := range functionReasons {
				fileReasons[reason] = true
			}
		}
		if file.Status != detailStatusForReasons(fileReasons) || !sameDetailReasons(file.Reasons, fileReasons) {
			return false
		}
		for reason := range fileReasons {
			projectReasons[reason] = true
		}
	}
	return index.Project.Status == detailStatusForReasons(projectReasons) && sameDetailReasons(index.Project.Reasons, projectReasons)
}
func cloneDetailReasonSet(values map[string]bool) map[string]bool {
	copy := make(map[string]bool, len(values)+2)
	for reason := range values {
		copy[reason] = true
	}
	return copy
}
func detailStatusForReasons(reasons map[string]bool) coveragedetail.Status {
	if len(reasons) == 0 {
		return coveragedetail.StatusCurrent
	}
	return coveragedetail.StatusIncomplete
}
func sameDetailReasons(actual []string, expected map[string]bool) bool {
	if len(actual) != len(expected) {
		return false
	}
	seen := map[string]bool{}
	for _, reason := range actual {
		if !expected[reason] || seen[reason] {
			return false
		}
		seen[reason] = true
	}
	return true
}
func mustDetailJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func insertDetailReasons(ctx context.Context, tx *sql.Tx, reportID, kind, id string, reasons []string) error {
	seen := map[string]bool{}
	var fileID, functionID any
	if kind == "file" {
		fileID = id
	} else if kind == "function" {
		functionID = id
	}
	for _, reason := range reasons {
		if reason == "" || seen[reason] {
			return task.ErrInvalidArgument
		}
		seen[reason] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO coverage_detail_reasons(report_id,owner_kind,owner_id,file_id,function_id,reason) VALUES(?,?,?,?,?,?)`, reportID, kind, id, fileID, functionID, reason); err != nil {
			return storageError("insert CoverageDetail reason", err)
		}
	}
	return nil
}

func (s *Store) GetCoverageProject(ctx context.Context, reportID string) (coveragedetail.Project, error) {
	if s != nil && !s.detailAvailable {
		return coveragedetail.Project{}, task.ErrStorageUnavailable
	}
	if s == nil || ctx == nil || !lowerHex(reportID, 32) {
		return coveragedetail.Project{}, task.ErrInvalidArgument
	}
	report, err := s.GetCoverageReport(ctx, reportID)
	if err != nil {
		return coveragedetail.Project{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return coveragedetail.Project{}, storageError("begin CoverageDetail read", err)
	}
	defer tx.Rollback()
	h, err := readDetailHeader(ctx, tx, report)
	if err != nil {
		return coveragedetail.Project{}, err
	}
	if err := tx.Commit(); err != nil {
		return coveragedetail.Project{}, storageError("commit CoverageDetail read", err)
	}
	return h.projectValue, nil
}

func readDetailHeader(ctx context.Context, tx *sql.Tx, report coveragedomain.Report) (detailHeader, error) {
	var h detailHeader
	var toolchain, summary, delta []byte
	var status string
	err := tx.QueryRowContext(ctx, `SELECT d.workspace_generation,d.project_id,d.coverage_run_id,d.toolchain_json,d.project_summary_json,d.project_delta_json,d.project_status,d.cursor_key FROM coverage_detail_reports d WHERE d.report_id=?`, report.ID).Scan(&h.workspace, &h.project, &h.runID, &toolchain, &summary, &delta, &status, &h.key)
	if isNoRows(err) {
		return detailHeader{}, task.ErrNotFound
	}
	if err != nil {
		return detailHeader{}, storageError("read CoverageDetail header", err)
	}
	var liveWorkspace, liveProject string
	if err := tx.QueryRowContext(ctx, `SELECT workspace_generation,project_id FROM coverage_runs WHERE coverage_run_id=?`, report.RunID).Scan(&liveWorkspace, &liveProject); err != nil {
		return detailHeader{}, storageError("read CoverageDetail owner", err)
	}
	_, _, liveToolchain, err := encodeCoverageReportMetadata(report)
	if err != nil || h.runID != report.RunID || h.workspace != liveWorkspace || h.project != liveProject || !bytes.Equal(toolchain, liveToolchain) || len(h.key) != 32 {
		return detailHeader{}, storageError("validate CoverageDetail report binding", err)
	}
	if err := decodeDetailCanonical(summary, &h.projectValue.Summary); err != nil {
		return detailHeader{}, storageError("read CoverageDetail summary", err)
	}
	if err := decodeDetailCanonical(delta, &h.projectValue.Delta); err != nil {
		return detailHeader{}, storageError("read CoverageDetail delta", err)
	}
	h.projectValue.Status = coveragedetail.Status(status)
	if !validDetailProject(h.projectValue) || h.projectValue.Status == coveragedetail.StatusCurrent && (report.Completeness.Outcome != coveragedomain.OutcomeAvailable || h.projectValue.Summary != report.Summary) {
		return detailHeader{}, storageError("validate CoverageDetail project", nil)
	}
	h.projectValue.Reasons, err = readDetailReasons(ctx, tx, report.ID, "project", report.ID)
	if err != nil {
		return detailHeader{}, err
	}
	return h, nil
}
func decodeDetailCanonical(raw []byte, v any) error {
	if err := decodeStrictJSON(raw, v); err != nil {
		return err
	}
	encoded, err := json.Marshal(v)
	if err != nil || !bytes.Equal(raw, encoded) {
		return errors.New("noncanonical detail JSON")
	}
	return nil
}
func readDetailReasons(ctx context.Context, tx *sql.Tx, reportID, kind, id string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT reason FROM coverage_detail_reasons WHERE report_id=? AND owner_kind=? AND owner_id=? ORDER BY reason LIMIT 129`, reportID, kind, id)
	if err != nil {
		return nil, storageError("read CoverageDetail reasons", err)
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var reason string
		if err := rows.Scan(&reason); err != nil || reason == "" {
			return nil, storageError("scan CoverageDetail reason", err)
		}
		result = append(result, reason)
		if len(result) > 128 {
			return nil, storageError("CoverageDetail reason bound", nil)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, storageError("read CoverageDetail reasons", err)
	}
	return result, nil
}

func detailScope(kind, reportID, workspace, parent, filter, sort string, limit int) string {
	b, _ := json.Marshal([]any{kind, reportID, workspace, parent, filter, sort, limit})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func encodeDetailCursor(key []byte, scope, last string) string {
	message := scope + "\x00" + last
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	b, _ := json.Marshal(detailCursor{Scope: scope, Last: last, MAC: hex.EncodeToString(mac.Sum(nil))})
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeDetailCursor(key []byte, scope, value string) (string, error) {
	if len(value) > 2048 {
		return "", task.ErrInvalidArgument
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", task.ErrInvalidArgument
	}
	if base64.RawURLEncoding.EncodeToString(b) != value {
		return "", task.ErrInvalidArgument
	}
	var c detailCursor
	if err := decodeStrictJSON(b, &c); err != nil || c.Scope != scope || c.Last == "" || len(c.Last) > 4096 {
		return "", task.ErrInvalidArgument
	}
	canonical, _ := json.Marshal(c)
	if !bytes.Equal(b, canonical) {
		return "", task.ErrInvalidArgument
	}
	want := hmac.New(sha256.New, key)
	want.Write([]byte(c.Scope + "\x00" + c.Last))
	got, err := hex.DecodeString(c.MAC)
	if err != nil || c.MAC != hex.EncodeToString(got) || !hmac.Equal(got, want.Sum(nil)) {
		return "", task.ErrInvalidArgument
	}
	return c.Last, nil
}
func validDetailQuery(reportID, workspace, sort string, limit, max int) bool {
	return lowerHex(reportID, 32) && lowerHex(workspace, 64) && limit >= 1 && limit <= max && (sort == "" || sort == "asc" || sort == "desc")
}
func detailOrder(sort string) string {
	if sort == "desc" {
		return "DESC"
	}
	return "ASC"
}

func (s *Store) detailRead(ctx context.Context, reportID, workspace string) (*sql.Tx, detailHeader, error) {
	report, err := s.GetCoverageReport(ctx, reportID)
	if err != nil {
		return nil, detailHeader{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, detailHeader{}, storageError("begin CoverageDetail read", err)
	}
	h, err := readDetailHeader(ctx, tx, report)
	if err != nil {
		tx.Rollback()
		return nil, detailHeader{}, err
	}
	if workspace != h.workspace {
		tx.Rollback()
		return nil, detailHeader{}, task.ErrInvalidArgument
	}
	return tx, h, nil
}

func (s *Store) ListCoverageFiles(ctx context.Context, q coveragedetail.FileQuery) (coveragedetail.FilePage, error) {
	if s != nil && !s.detailAvailable {
		return coveragedetail.FilePage{}, task.ErrStorageUnavailable
	}
	if s == nil || ctx == nil || !validDetailQuery(q.ReportID, q.WorkspaceGeneration, q.Sort, q.Limit, detailFileLimit) {
		return coveragedetail.FilePage{}, task.ErrInvalidArgument
	}
	tx, h, err := s.detailRead(ctx, q.ReportID, q.WorkspaceGeneration)
	if err != nil {
		return coveragedetail.FilePage{}, err
	}
	defer tx.Rollback()
	scope := detailScope("files", q.ReportID, q.WorkspaceGeneration, "", q.Filter, q.Sort, q.Limit)
	last := ""
	if q.Cursor != "" {
		last, err = decodeDetailCursor(h.key, scope, q.Cursor)
		if err != nil {
			return coveragedetail.FilePage{}, err
		}
	}
	op := ">"
	if q.Sort == "desc" {
		op = "<"
	}
	query := `SELECT file_id,relative_path,source_sha256,summary_json,delta_json,status FROM coverage_detail_files WHERE report_id=? AND instr(relative_path,?)>0`
	args := []any{q.ReportID, q.Filter}
	if last != "" {
		query += ` AND relative_path ` + op + ` ?`
		args = append(args, last)
	}
	query += ` ORDER BY relative_path ` + detailOrder(q.Sort) + `,file_id ` + detailOrder(q.Sort) + ` LIMIT ?`
	args = append(args, q.Limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return coveragedetail.FilePage{}, storageError("list CoverageDetail files", err)
	}
	items := []coveragedetail.File{}
	for rows.Next() {
		var f coveragedetail.File
		var summary, delta []byte
		var status string
		if err := rows.Scan(&f.ID, &f.RelativePath, &f.SourceSHA256, &summary, &delta, &status); err != nil {
			rows.Close()
			return coveragedetail.FilePage{}, storageError("scan CoverageDetail file", err)
		}
		if err := decodeDetailCanonical(summary, &f.Summary); err != nil {
			rows.Close()
			return coveragedetail.FilePage{}, storageError("decode CoverageDetail file", err)
		}
		if err := decodeDetailCanonical(delta, &f.Delta); err != nil {
			rows.Close()
			return coveragedetail.FilePage{}, storageError("decode CoverageDetail delta", err)
		}
		f.Status = coveragedetail.Status(status)
		if !validDetailStatus(f.Status) || !validDetailSummary(f.Summary) || !validDetailDelta(f.Delta) {
			rows.Close()
			return coveragedetail.FilePage{}, storageError("validate CoverageDetail file", nil)
		}
		items = append(items, f)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return coveragedetail.FilePage{}, storageError("list CoverageDetail files", err)
	}
	rows.Close()
	page := coveragedetail.FilePage{Items: items}
	if len(items) > q.Limit {
		page.Items = items[:q.Limit]
		page.NextCursor = encodeDetailCursor(h.key, scope, page.Items[len(page.Items)-1].RelativePath)
	}
	for i := range page.Items {
		page.Items[i].Reasons, err = readDetailReasons(ctx, tx, q.ReportID, "file", page.Items[i].ID)
		if err != nil {
			return coveragedetail.FilePage{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return coveragedetail.FilePage{}, storageError("commit CoverageDetail read", err)
	}
	return page, nil
}

func (s *Store) ListCoverageFunctions(ctx context.Context, q coveragedetail.FunctionQuery) (coveragedetail.FunctionPage, error) {
	if s != nil && !s.detailAvailable {
		return coveragedetail.FunctionPage{}, task.ErrStorageUnavailable
	}
	if s == nil || ctx == nil || !validDetailQuery(q.ReportID, q.WorkspaceGeneration, q.Sort, q.Limit, detailFunctionLimit) || !lowerHex(q.FileID, 32) {
		return coveragedetail.FunctionPage{}, task.ErrInvalidArgument
	}
	tx, h, err := s.detailRead(ctx, q.ReportID, q.WorkspaceGeneration)
	if err != nil {
		return coveragedetail.FunctionPage{}, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM coverage_detail_files WHERE report_id=? AND file_id=?`, q.ReportID, q.FileID).Scan(&exists); isNoRows(err) {
		return coveragedetail.FunctionPage{}, task.ErrNotFound
	} else if err != nil {
		return coveragedetail.FunctionPage{}, storageError("validate CoverageDetail file parent", err)
	}
	scope := detailScope("functions", q.ReportID, q.WorkspaceGeneration, q.FileID, q.Filter, q.Sort, q.Limit)
	last := ""
	if q.Cursor != "" {
		last, err = decodeDetailCursor(h.key, scope, q.Cursor)
		if err != nil {
			return coveragedetail.FunctionPage{}, err
		}
	}
	var lastName, lastID string
	if last != "" {
		var pair [2]string
		if err := json.Unmarshal([]byte(last), &pair); err != nil || !lowerHex(pair[1], 32) {
			return coveragedetail.FunctionPage{}, task.ErrInvalidArgument
		}
		lastName, lastID = pair[0], pair[1]
	}
	op := ">"
	if q.Sort == "desc" {
		op = "<"
	}
	query := `SELECT function_id,name,linkage_name,signature_digest,start_line,start_column,end_line,end_column,summary_json,delta_json,status FROM coverage_detail_functions WHERE report_id=? AND file_id=? AND instr(name,?)>0`
	args := []any{q.ReportID, q.FileID, q.Filter}
	if last != "" {
		query += ` AND (name ` + op + ` ? OR (name=? AND function_id ` + op + ` ?))`
		args = append(args, lastName, lastName, lastID)
	}
	query += ` ORDER BY name ` + detailOrder(q.Sort) + `,function_id ` + detailOrder(q.Sort) + ` LIMIT ?`
	args = append(args, q.Limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return coveragedetail.FunctionPage{}, storageError("list CoverageDetail functions", err)
	}
	items := []coveragedetail.Function{}
	for rows.Next() {
		var f coveragedetail.Function
		var summary, delta []byte
		var status string
		if err := rows.Scan(&f.ID, &f.Name, &f.LinkageName, &f.SignatureDigest, &f.Start.Line, &f.Start.Column, &f.End.Line, &f.End.Column, &summary, &delta, &status); err != nil {
			rows.Close()
			return coveragedetail.FunctionPage{}, storageError("scan CoverageDetail function", err)
		}
		if err := decodeDetailCanonical(summary, &f.Summary); err != nil {
			rows.Close()
			return coveragedetail.FunctionPage{}, storageError("decode CoverageDetail function", err)
		}
		if err := decodeDetailCanonical(delta, &f.Delta); err != nil {
			rows.Close()
			return coveragedetail.FunctionPage{}, storageError("decode CoverageDetail delta", err)
		}
		f.Status = coveragedetail.Status(status)
		if !validDetailStatus(f.Status) || !validDetailSummary(f.Summary) || !validDetailDelta(f.Delta) {
			rows.Close()
			return coveragedetail.FunctionPage{}, storageError("validate CoverageDetail function", nil)
		}
		items = append(items, f)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return coveragedetail.FunctionPage{}, storageError("list CoverageDetail functions", err)
	}
	rows.Close()
	page := coveragedetail.FunctionPage{Items: items}
	if len(items) > q.Limit {
		page.Items = items[:q.Limit]
		last := page.Items[len(page.Items)-1]
		b, _ := json.Marshal([2]string{last.Name, last.ID})
		page.NextCursor = encodeDetailCursor(h.key, scope, string(b))
	}
	for i := range page.Items {
		page.Items[i].Reasons, err = readDetailReasons(ctx, tx, q.ReportID, "function", page.Items[i].ID)
		if err != nil {
			return coveragedetail.FunctionPage{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return coveragedetail.FunctionPage{}, storageError("commit CoverageDetail read", err)
	}
	return page, nil
}

func (s *Store) ListCoverageLines(ctx context.Context, q coveragedetail.LineQuery) (coveragedetail.LinePage, error) {
	if s != nil && !s.detailAvailable {
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
	if q.FileID == "" {
		if err := tx.QueryRowContext(ctx, `SELECT file_id FROM coverage_detail_functions WHERE report_id=? AND function_id=?`, q.ReportID, q.FunctionID).Scan(&q.FileID); isNoRows(err) {
			return coveragedetail.LinePage{}, task.ErrNotFound
		} else if err != nil {
			return coveragedetail.LinePage{}, storageError("resolve CoverageDetail function", err)
		}
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM coverage_detail_files WHERE report_id=? AND file_id=?`, q.ReportID, q.FileID).Scan(&exists); isNoRows(err) {
		return coveragedetail.LinePage{}, task.ErrNotFound
	} else if err != nil {
		return coveragedetail.LinePage{}, storageError("validate CoverageDetail file parent", err)
	}
	if q.FunctionID != "" {
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM coverage_detail_functions WHERE report_id=? AND file_id=? AND function_id=?`, q.ReportID, q.FileID, q.FunctionID).Scan(&exists); isNoRows(err) {
			return coveragedetail.LinePage{}, task.ErrNotFound
		} else if err != nil {
			return coveragedetail.LinePage{}, storageError("validate CoverageDetail function parent", err)
		}
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
	items := []coveragedetail.Line{}
	for rows.Next() {
		var line coveragedetail.Line
		if err := rows.Scan(&line.Line, &line.Count); err != nil || !validDetailLine(line) {
			rows.Close()
			return coveragedetail.LinePage{}, storageError("scan CoverageDetail line", err)
		}
		items = append(items, line)
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
