package auth

import "sort"

// Permission is a named capability. Business logic checks permissions, never role names.
type Permission string

const (
	// Platform-level.
	PermPlatformAdmin         Permission = "platform.admin"
	PermOpsApplicationsRead   Permission = "ops.applications.read"
	PermOpsApplicationsReview Permission = "ops.applications.review"
	PermOpsInstitutesManage   Permission = "ops.institutes.manage"
	PermOpsAuditRead          Permission = "ops.audit.read"

	// Institute-level (granted through institute membership; wired in COS-008).
	PermInstituteRead   Permission = "institute.read"
	PermInstituteManage Permission = "institute.manage"
	PermStaffManage     Permission = "staff.manage"
	PermCoursesRead     Permission = "courses.read"
	PermCoursesWrite    Permission = "courses.write"
	PermStudentsRead    Permission = "students.read"
	PermStudentsWrite   Permission = "students.write"
	PermStudentsImport  Permission = "students.import"
	PermStudentsExport  Permission = "students.export"
	PermTemplatesRead   Permission = "templates.read"
	PermTemplatesWrite  Permission = "templates.write"
	PermCertsRead       Permission = "certificates.read"
	PermCertsIssue      Permission = "certificates.issue"
	PermCertsRevoke     Permission = "certificates.revoke"
	PermCertsDeliver    Permission = "certificates.deliver"
)

// PlatformRole is a platform-wide role stored on the user. Empty means a normal user.
type PlatformRole string

const (
	PlatformNone       PlatformRole = ""
	PlatformSuperAdmin PlatformRole = "super_admin"
	PlatformOps        PlatformRole = "ops"
)

func (r PlatformRole) Valid() bool {
	return r == PlatformNone || r == PlatformSuperAdmin || r == PlatformOps
}

// InstituteRole is a role within one institute (assigned via membership, COS-008).
type InstituteRole string

const (
	InstituteOwner InstituteRole = "owner"
	InstituteAdmin InstituteRole = "admin"
	InstituteStaff InstituteRole = "staff"
)

func (r InstituteRole) Valid() bool {
	return r == InstituteOwner || r == InstituteAdmin || r == InstituteStaff
}

type permSet map[Permission]struct{}

func set(ps ...Permission) permSet {
	s := make(permSet, len(ps))
	for _, p := range ps {
		s[p] = struct{}{}
	}
	return s
}

func merge(sets ...permSet) permSet {
	out := permSet{}
	for _, s := range sets {
		for p := range s {
			out[p] = struct{}{}
		}
	}
	return out
}

var instituteOperational = set(
	PermInstituteRead, PermCoursesRead, PermCoursesWrite, PermStudentsRead, PermStudentsWrite,
	PermStudentsImport, PermStudentsExport, PermTemplatesRead, PermTemplatesWrite,
	PermCertsRead, PermCertsIssue, PermCertsRevoke, PermCertsDeliver,
)

// Platform ops deliberately have NO student or certificate permissions (brain §3.3).
var platformRolePerms = map[PlatformRole]permSet{
	PlatformSuperAdmin: set(PermPlatformAdmin, PermOpsApplicationsRead, PermOpsApplicationsReview,
		PermOpsInstitutesManage, PermOpsAuditRead),
	PlatformOps: set(PermOpsApplicationsRead, PermOpsApplicationsReview, PermOpsInstitutesManage),
}

// Certificate staff get operational read/write by default but must be explicitly granted
// issuance/revocation/delivery (brain §3.3); per-membership grants arrive with COS-008.
var instituteRolePerms = map[InstituteRole]permSet{
	InstituteOwner: merge(instituteOperational, set(PermInstituteManage, PermStaffManage)),
	InstituteAdmin: instituteOperational,
	InstituteStaff: set(PermInstituteRead, PermCoursesRead, PermCoursesWrite, PermStudentsRead,
		PermStudentsWrite, PermTemplatesRead, PermCertsRead),
}

// PlatformPermissions returns the permissions of a platform role (unknown roles get none).
func PlatformPermissions(r PlatformRole) []Permission { return sorted(platformRolePerms[r]) }

// InstitutePermissions returns the default permissions of an institute role (unknown roles get none).
func InstitutePermissions(r InstituteRole) []Permission { return sorted(instituteRolePerms[r]) }

func sorted(s permSet) []Permission {
	out := make([]Permission, 0, len(s))
	for p := range s {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// HasPlatformPermission fails closed: unknown roles and unknown permissions are denied.
func HasPlatformPermission(r PlatformRole, p Permission) bool {
	_, ok := platformRolePerms[r][p]
	return ok
}

// HasInstitutePermission fails closed.
func HasInstitutePermission(r InstituteRole, p Permission) bool {
	_, ok := instituteRolePerms[r][p]
	return ok
}
