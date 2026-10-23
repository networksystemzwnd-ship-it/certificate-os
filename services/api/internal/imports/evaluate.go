package imports

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/certificate-os/api/internal/records"
)

// Existing describes students already in the institute, keyed for duplicate detection.
type Existing struct {
	Numbers map[string]bool     // lowercase student numbers (archived students included)
	Emails  map[string][]string // lowercase email -> lowercase full names
}

func NewExisting(students []records.Student) Existing {
	e := Existing{Numbers: map[string]bool{}, Emails: map[string][]string{}}
	for _, s := range students {
		if s.StudentNumber != "" {
			e.Numbers[strings.ToLower(s.StudentNumber)] = true
		}
		if s.Email != "" {
			e.Emails[strings.ToLower(s.Email)] = append(e.Emails[strings.ToLower(s.Email)], strings.ToLower(s.FullName))
		}
	}
	return e
}

// Plan is what confirm will write for a valid row.
type Plan struct {
	Student records.StudentInput
	// Enrollment is optional: BatchID != "" means enroll in that batch with Status/CompletionDate/Grade.
	BatchID        string
	Status         records.EnrollmentStatus
	CompletionDate string
	Grade          string
}

func cell(row []string, m Mapping, field string) string {
	i, ok := m[field]
	if !ok || i < 0 || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

// Keys returns the lowercase student numbers and emails present in the mapped columns, so the caller can
// look up existing students in one go.
func Keys(imp Import) (numbers, emails []string) {
	ns, es := map[string]bool{}, map[string]bool{}
	for _, row := range imp.Rows {
		if n := strings.ToLower(cell(row, imp.Mapping, FieldStudentNumber)); n != "" && !ns[n] {
			ns[n] = true
			numbers = append(numbers, n)
		}
		if e := strings.ToLower(cell(row, imp.Mapping, FieldEmail)); e != "" && !es[e] {
			es[e] = true
			emails = append(emails, e)
		}
	}
	sort.Strings(numbers)
	sort.Strings(emails)
	return
}

// ParseDate accepts the declared format only; day/month order is never guessed. Slashes, dashes and
// dots are interchangeable separators for dmy and mdy; two-digit years are rejected.
func ParseDate(s, format string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", true
	}
	var layout string
	switch format {
	case "dmy":
		layout = "2/1/2006"
	case "mdy":
		layout = "1/2/2006"
	default:
		layout = "2006-01-02"
	}
	if format == "dmy" || format == "mdy" {
		s = strings.NewReplacer("-", "/", ".", "/").Replace(s)
	}
	t, err := time.Parse(layout, s)
	if err != nil || t.Year() < 1900 || t.Year() > 2200 {
		return "", false
	}
	return t.Format("2006-01-02"), true
}

func normStatus(s string) (records.EnrollmentStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return "", true
	case "enrolled", "enrolled / ongoing", "ongoing", "active":
		return records.Enrolled, true
	case "completed", "complete", "passed":
		return records.Completed, true
	case "withdrawn", "dropped", "dropout":
		return records.Withdrawn, true
	}
	return "", false
}

// resolveBatch finds exactly one active batch by name (and course name or code when given).
func resolveBatch(batches []records.Batch, courseCodes map[string]string, batch, course string) (string, string) {
	bl, cl := strings.ToLower(batch), strings.ToLower(course)
	var found []records.Batch
	for _, b := range batches {
		if strings.ToLower(b.Name) != bl {
			continue
		}
		if cl != "" && strings.ToLower(b.CourseName) != cl && strings.ToLower(courseCodes[b.CourseID]) != cl {
			continue
		}
		found = append(found, b)
	}
	switch len(found) {
	case 0:
		if cl != "" {
			return "", "batch: No active batch with this name exists in that course."
		}
		return "", "batch: No active batch with this name exists."
	case 1:
		return found[0].ID, ""
	}
	return "", "batch: Several courses have a batch with this name; map a Course column too."
}

// Evaluate validates every row against the mapping, the catalog of active batches and the existing
// students, without writing anything. prior carries results of an interrupted confirm: rows already
// imported are kept as they are and not evaluated again. The returned plans are aligned with results
// (a plan is only meaningful for RowValid rows).
func Evaluate(imp Import, batches []records.Batch, courseCodes map[string]string, ex Existing, prior []RowResult) ([]RowResult, []Plan) {
	results := make([]RowResult, len(imp.Rows))
	plans := make([]Plan, len(imp.Rows))
	seenNum := map[string]int{}
	seenMail := map[string][]string{}
	for i, row := range imp.Rows {
		res := RowResult{Row: i + 2, State: RowValid}
		if i < len(prior) && prior[i].State == RowImported {
			results[i] = prior[i]
			continue
		}
		in := records.StudentInput{
			FullName: cell(row, imp.Mapping, FieldFullName), StudentNumber: cell(row, imp.Mapping, FieldStudentNumber),
			Email: cell(row, imp.Mapping, FieldEmail), Phone: cell(row, imp.Mapping, FieldPhone),
		}
		var problems []string
		add := func(field, msg string) { problems = append(problems, field+": "+msg) }
		norm, err := records.NormalizeStudent(in)
		var ve *records.ValidationError
		if errors.As(err, &ve) {
			for _, f := range []string{"full_name", "student_number", "email", "phone"} {
				if m := ve.Problems[f]; m != "" {
					add(f, m)
				}
			}
		}
		plan := Plan{Student: norm}

		courseName, batchName := cell(row, imp.Mapping, FieldCourse), cell(row, imp.Mapping, FieldBatch)
		statusRaw, dateRaw, grade := cell(row, imp.Mapping, FieldStatus), cell(row, imp.Mapping, FieldCompletionDate), cell(row, imp.Mapping, FieldGrade)
		status, okStatus := normStatus(statusRaw)
		if !okStatus {
			add("status", "Use enrolled, completed or withdrawn.")
		}
		date, okDate := ParseDate(dateRaw, imp.DateFormat)
		if !okDate {
			add("completion_date", "Not a valid date in the chosen date format.")
		}
		if batchName == "" {
			if courseName != "" || statusRaw != "" || dateRaw != "" || grade != "" {
				add("batch", "A batch is needed to record a course, status, completion date or grade.")
			}
		} else if okStatus && okDate {
			if status == "" {
				status = records.Enrolled
				if date != "" {
					status = records.Completed
				}
			}
			if id, msg := resolveBatch(batches, courseCodes, batchName, courseName); msg != "" {
				problems = append(problems, msg)
			} else if err := records.ValidateEnrollmentState(status, date, grade); err != nil {
				var eve *records.ValidationError
				if errors.As(err, &eve) {
					for _, f := range []string{"status", "completion_date", "grade"} {
						if m := eve.Problems[f]; m != "" {
							add(f, m)
						}
					}
				}
			} else {
				plan.BatchID, plan.Status, plan.CompletionDate, plan.Grade = id, status, date, grade
			}
		}
		if len(problems) > 0 {
			res.State, res.Message = RowError, strings.Join(problems, " ")
			results[i], plans[i] = res, plan
			continue
		}

		num, mail, name := strings.ToLower(norm.StudentNumber), norm.Email, strings.ToLower(norm.FullName)
		switch {
		case num != "" && ex.Numbers[num]:
			res.State, res.Message = RowDuplicate, "A student with this student number already exists; not changed."
		case num != "" && seenNum[num] > 0:
			res.State, res.Message = RowDuplicate, "Same student number as row "+itoa(seenNum[num])+"; skipped."
		case mail != "" && contains(ex.Emails[mail], name):
			res.State, res.Message = RowDuplicate, "A student with this name and email already exists; not changed."
		case mail != "" && contains(seenMail[mail], name):
			res.State, res.Message = RowDuplicate, "Same name and email as an earlier row; skipped."
		default:
			if mail != "" && len(ex.Emails[mail]) > 0 {
				res.Message = "Shares an email address with an existing student (different name); will be imported."
			}
			if num != "" {
				seenNum[num] = res.Row
			}
			if mail != "" {
				seenMail[mail] = append(seenMail[mail], name)
			}
		}
		results[i], plans[i] = res, plan
	}
	return results, plans
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// Tally counts results by state.
func Tally(results []RowResult) Counts {
	c := Counts{Total: len(results)}
	for _, r := range results {
		switch r.State {
		case RowValid:
			c.Valid++
		case RowError:
			c.Errors++
		case RowDuplicate:
			c.Duplicates++
		case RowImported:
			c.Imported++
		case RowFailed:
			c.Failed++
		}
	}
	return c
}
