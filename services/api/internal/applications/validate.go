package applications

import (
	"net/url"
	"regexp"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/tenancy"
)

var Categories = []string{"training_institute", "academy", "vocational_centre", "school", "college", "other"}

var (
	countryRe = regexp.MustCompile(`^[A-Z]{2}$`)
	phoneRe   = regexp.MustCompile(`^\+?[0-9][0-9 ()-]{5,18}[0-9]$`)
)

func validCategory(c string) bool {
	for _, v := range Categories {
		if c == v {
			return true
		}
	}
	return false
}

func length(s string, min, max int) bool { n := len([]rune(s)); return n >= min && n <= max }

// Patch updates a subset of details. Nil pointers mean "leave unchanged".
type Patch struct {
	Name, Slug, Category, AddressLine1, AddressLine2, City, Region *string
	PostalCode, Country, OfficialEmail, Phone, RepName, RepRole    *string
	Website                                                        *string
	Attested                                                       *bool
}

// Apply merges a patch onto d (call Normalize afterwards).
func (d Details) Apply(p Patch) Details {
	set := func(dst, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	set(&d.Name, p.Name)
	set(&d.Slug, p.Slug)
	set(&d.Category, p.Category)
	set(&d.AddressLine1, p.AddressLine1)
	set(&d.AddressLine2, p.AddressLine2)
	set(&d.City, p.City)
	set(&d.Region, p.Region)
	set(&d.PostalCode, p.PostalCode)
	set(&d.Country, p.Country)
	set(&d.OfficialEmail, p.OfficialEmail)
	set(&d.Phone, p.Phone)
	set(&d.RepName, p.RepName)
	set(&d.RepRole, p.RepRole)
	set(&d.Website, p.Website)
	if p.Attested != nil {
		d.Attested = *p.Attested
	}
	return d
}

// ValidateFormat checks every PROVIDED field (drafts may be incomplete). d must be normalized.
// Keys of the returned map are the API field names.
func ValidateFormat(d Details) map[string]string {
	p := map[string]string{}
	if d.Name != "" && !length(d.Name, 2, 200) {
		p["name"] = "Enter the institute's name (2–200 characters)."
	}
	if d.Slug != "" && tenancy.ValidateSlug(d.Slug) != nil {
		p["slug"] = "Use 3–40 lowercase letters, digits or hyphens (not starting or ending with a hyphen); some names are reserved."
	}
	if d.Category != "" && !validCategory(d.Category) {
		p["category"] = "Choose one of the listed categories."
	}
	if d.AddressLine1 != "" && !length(d.AddressLine1, 3, 200) {
		p["address_line1"] = "Enter a street address (3–200 characters)."
	}
	if !length(d.AddressLine2, 0, 200) {
		p["address_line2"] = "Address line 2 is too long."
	}
	if !length(d.City, 0, 100) {
		p["city"] = "City is too long."
	}
	if !length(d.Region, 0, 100) {
		p["region"] = "Region is too long."
	}
	if !length(d.PostalCode, 0, 20) {
		p["postal_code"] = "Postal code is too long."
	}
	if d.Country != "" && !countryRe.MatchString(d.Country) {
		p["country"] = "Use a two-letter country code, e.g. IN."
	}
	if d.OfficialEmail != "" {
		if _, ok := auth.NormalizeEmail(d.OfficialEmail); !ok {
			p["official_email"] = "Enter a valid email address."
		}
	}
	if d.Phone != "" && !phoneRe.MatchString(d.Phone) {
		p["phone"] = "Enter a valid phone number (digits, spaces, +, -, parentheses)."
	}
	if d.RepName != "" && !length(d.RepName, 2, 120) {
		p["rep_name"] = "Enter the representative's full name."
	}
	if d.RepRole != "" && !length(d.RepRole, 2, 120) {
		p["rep_role"] = "Enter the representative's role."
	}
	if d.Website != "" {
		u, err := url.Parse(d.Website)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || len(d.Website) > 300 {
			p["website"] = "Enter a full web address starting with http:// or https://."
		}
	}
	return p
}

// CheckSubmittable reports every problem that blocks submission: format errors, missing required
// fields and a missing attestation.
func CheckSubmittable(d Details) map[string]string {
	p := ValidateFormat(d)
	required := map[string]string{
		"name": d.Name, "slug": d.Slug, "category": d.Category, "address_line1": d.AddressLine1,
		"city": d.City, "country": d.Country, "official_email": d.OfficialEmail, "phone": d.Phone,
		"rep_name": d.RepName, "rep_role": d.RepRole,
	}
	for k, v := range required {
		if v == "" && p[k] == "" {
			p[k] = "This field is required."
		}
	}
	if !d.Attested {
		p["attested"] = "You must confirm that the information is accurate and that you are authorized to apply."
	}
	return p
}
