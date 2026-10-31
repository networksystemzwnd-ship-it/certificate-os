// Package verification is the PUBLIC side of certificates (M5a): anyone holding a certificate's verification
// link can see whether it is valid, revoked or replaced, without an account.
//
// It is deliberately narrow. The public response contains only approved fields (D-160): authenticity status,
// certificate number, institute name, student name, course, issue date, and the revocation date or the link to the
// replacement. It never contains contact data, grade, completion date, internal ids, revocation reasons or the
// PDF. Pending and failed certificates do not exist publicly. An id that is malformed, unknown or not issued gets
// the same 404, so the endpoint reveals nothing about other records. The id is a 256-bit random value (D-146).
package verification

import (
	"context"
	"errors"
	"regexp"
	"time"
)

var ErrNotFound = errors.New("certificate not found")

// Status is what a verifier sees.
type Status string

const (
	Valid    Status = "valid"
	Revoked  Status = "revoked"
	Replaced Status = "replaced"
)

// Record is everything the store may hand to the public layer; the service copies out the approved fields.
type Record struct {
	Number             string
	InstituteName      string
	StudentName        string
	CourseName         string
	IssueDate          string // YYYY-MM-DD, as printed
	Revoked            bool
	RevokedAt          *time.Time
	ReplacedByPublicID string // set when the certificate was superseded
}

// StatusOf is the single place that decides the public status.
func StatusOf(r Record) Status {
	switch {
	case r.Revoked && r.ReplacedByPublicID != "":
		return Replaced
	case r.Revoked:
		return Revoked
	}
	return Valid
}

// Store looks a certificate up by its public id across institutes (no tenant context) and records the
// verification (certificate and result only). Only issued and revoked certificates exist publicly.
type Store interface {
	Lookup(ctx context.Context, publicID string) (Record, error)
}

// Result is the public response.
type Result struct {
	Status             Status
	CertificateNumber  string
	InstituteName      string
	StudentName        string
	CourseName         string
	IssueDate          string
	RevokedAt          *time.Time // revoked and replaced only
	ReplacedByPublicID string     // replaced only: verification id of the replacement
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// ValidID reports whether s has the shape of a public id (checked before touching the database).
func ValidID(s string) bool { return idRe.MatchString(s) }

type Service struct{ Store Store }

func NewService(st Store) *Service { return &Service{Store: st} }

func (s *Service) Verify(ctx context.Context, publicID string) (Result, error) {
	if !ValidID(publicID) {
		return Result{}, ErrNotFound
	}
	rec, err := s.Store.Lookup(ctx, publicID)
	if err != nil {
		return Result{}, err
	}
	res := Result{Status: StatusOf(rec), CertificateNumber: rec.Number, InstituteName: rec.InstituteName, StudentName: rec.StudentName,
		CourseName: rec.CourseName, IssueDate: rec.IssueDate}
	if res.Status != Valid {
		res.RevokedAt = rec.RevokedAt
	}
	if res.Status == Replaced {
		res.ReplacedByPublicID = rec.ReplacedByPublicID
	}
	return res, nil
}
