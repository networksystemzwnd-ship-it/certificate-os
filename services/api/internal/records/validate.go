package records

import (
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"
)

var (
	studentNumberRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,39}$`)
	phoneRe         = regexp.MustCompile(`^\+?[0-9][0-9 ()-]{5,18}[0-9]$`)
	codeRe          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,39}$`)
)

func runes(s string) int { return len([]rune(s)) }

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return true
		}
	}
	return false
}

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func normEmail(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", true
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || len(s) > 254 || strings.ContainsAny(s, " <>") {
		return s, false
	}
	return s, true
}

func problems(p map[string]string) error {
	if len(p) == 0 {
		return nil
	}
	return &ValidationError{Problems: p}
}

// NormalizeCourse trims input and validates it. Name is required.
func NormalizeCourse(in CourseInput) (CourseInput, error) {
	in.Name, in.Code, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Code), strings.TrimSpace(in.Description)
	p := map[string]string{}
	if n := runes(in.Name); n < 2 || n > 200 || hasControl(in.Name) {
		p["name"] = "Enter a course name (2–200 characters)."
	}
	if in.Code != "" && !codeRe.MatchString(in.Code) {
		p["code"] = "Use up to 40 letters, digits, dots, slashes, dashes or underscores."
	}
	if runes(in.Description) > 2000 {
		p["description"] = "Description is too long (max 2000 characters)."
	}
	return in, problems(p)
}

// NormalizeBatch validates a batch. CourseID is required on create only (checked by the service).
func NormalizeBatch(in BatchInput) (BatchInput, error) {
	in.Name, in.StartDate, in.EndDate = strings.TrimSpace(in.Name), strings.TrimSpace(in.StartDate), strings.TrimSpace(in.EndDate)
	p := map[string]string{}
	if n := runes(in.Name); n < 2 || n > 200 || hasControl(in.Name) {
		p["name"] = "Enter a batch name (2–200 characters)."
	}
	if in.StartDate != "" && !validDate(in.StartDate) {
		p["start_date"] = "Use the format YYYY-MM-DD."
	}
	if in.EndDate != "" && !validDate(in.EndDate) {
		p["end_date"] = "Use the format YYYY-MM-DD."
	}
	if p["start_date"] == "" && p["end_date"] == "" && in.StartDate != "" && in.EndDate != "" && in.EndDate < in.StartDate {
		p["end_date"] = "The end date cannot be before the start date."
	}
	return in, problems(p)
}

// NormalizeStudent validates a student. Only the full name is required (brain §3.4: contact data only when needed).
func NormalizeStudent(in StudentInput) (StudentInput, error) {
	in.StudentNumber, in.FullName, in.Phone = strings.TrimSpace(in.StudentNumber), strings.TrimSpace(in.FullName), strings.TrimSpace(in.Phone)
	p := map[string]string{}
	if n := runes(in.FullName); n < 2 || n > 200 || hasControl(in.FullName) {
		p["full_name"] = "Enter the student's full name (2–200 characters)."
	}
	if in.StudentNumber != "" && !studentNumberRe.MatchString(in.StudentNumber) {
		p["student_number"] = "Use up to 40 letters, digits, dots, slashes or dashes."
	}
	email, ok := normEmail(in.Email)
	in.Email = email
	if !ok {
		p["email"] = "Enter a valid email address."
	}
	if in.Phone != "" && !phoneRe.MatchString(in.Phone) {
		p["phone"] = "Enter a valid phone number (digits, spaces, +, -, parentheses)."
	}
	return in, problems(p)
}

// ValidateEnrollmentState checks the state an enrollment will have AFTER a patch is applied.
func ValidateEnrollmentState(status EnrollmentStatus, completionDate, grade string) error {
	p := map[string]string{}
	if !status.Valid() {
		p["status"] = "Choose enrolled, completed or withdrawn."
	}
	if completionDate != "" && !validDate(completionDate) {
		p["completion_date"] = "Use the format YYYY-MM-DD."
	}
	if status == Completed && completionDate == "" {
		p["completion_date"] = "A completion date is required when the course is completed."
	}
	if status != Completed && status.Valid() && completionDate != "" {
		p["completion_date"] = "Only completed enrollments have a completion date."
	}
	if runes(grade) > 40 || hasControl(grade) {
		p["grade"] = "Grade or result is too long (max 40 characters)."
	}
	return problems(p)
}

// ApplyEnrollmentPatch computes the state an enrollment will have after p and validates it.
// Moving away from "completed" without an explicit date clears the completion date. Both stores use this
// so the rule lives in one place.
func ApplyEnrollmentPatch(cur Enrollment, p EnrollmentPatch) (EnrollmentStatus, string, string, error) {
	status, date, grade := cur.Status, cur.CompletionDate, cur.Grade
	if p.Status != nil {
		status = *p.Status
		if status != Completed && p.CompletionDate == nil {
			date = ""
		}
	}
	if p.CompletionDate != nil {
		date = *p.CompletionDate
	}
	if p.Grade != nil {
		grade = *p.Grade
	}
	if err := ValidateEnrollmentState(status, date, grade); err != nil {
		return cur.Status, cur.CompletionDate, cur.Grade, err
	}
	return status, date, grade, nil
}
