package auth

import "aagasa/internal/domain"

// Authority orders the roles. Root outranks Admin outranks a normal user
// (spec.md section 17).
func Authority(role domain.UserRole) int {
	switch role {
	case domain.RoleRoot:
		return 3
	case domain.RoleAdmin:
		return 2
	case domain.RoleUser:
		return 1
	default:
		return 0
	}
}

// IsValidRole reports whether a role is one Aagasa recognises.
func IsValidRole(role domain.UserRole) bool {
	return Authority(role) > 0
}

// AtLeast reports whether the actor holds at least the required role.
func AtLeast(actor domain.User, required domain.UserRole) bool {
	return Authority(actor.Role) >= Authority(required)
}

// CanApprovePasses reports whether the actor may approve pass requests. Used
// from V7; the rule belongs with the other role rules.
func CanApprovePasses(actor domain.User) bool {
	return AtLeast(actor, domain.RoleAdmin)
}

// CanManageUsers reports whether the actor may list and create accounts.
func CanManageUsers(actor domain.User) bool {
	return AtLeast(actor, domain.RoleAdmin)
}

// CanCreateUserWithRole reports whether the actor may create an account with
// the given role. Only Root may mint Admins, and nobody may create a second
// Root: the bootstrap account is the only one.
func CanCreateUserWithRole(actor domain.User, role domain.UserRole) bool {
	if !IsValidRole(role) || role == domain.RoleRoot {
		return false
	}
	if role == domain.RoleAdmin {
		return actor.Role == domain.RoleRoot
	}
	return CanManageUsers(actor)
}

// CanChangeRole reports whether the actor may move target to a new role.
//
// Role changes are Root-only, Root cannot be demoted, and no one can be
// promoted to Root. The database enforces the Root rules too; this keeps the
// API from reaching a query it will only be refused for.
func CanChangeRole(actor, target domain.User, newRole domain.UserRole) bool {
	if actor.Role != domain.RoleRoot {
		return false
	}
	if !IsValidRole(newRole) || newRole == domain.RoleRoot {
		return false
	}
	return target.Role != domain.RoleRoot
}

// CanDeleteUser reports whether the actor may delete the target account.
// Root is undeletable and no one may delete themselves.
func CanDeleteUser(actor, target domain.User) bool {
	if actor.Role != domain.RoleRoot {
		return false
	}
	if target.Role == domain.RoleRoot || target.ID == actor.ID {
		return false
	}
	return true
}

// CanConfigureStation reports whether the actor may change station and
// scheduling configuration. This is Root-only (spec.md section 17.1).
func CanConfigureStation(actor domain.User) bool {
	return actor.Role == domain.RoleRoot
}

// CanRunFirstRunSetup reports whether the actor may complete first-run setup.
func CanRunFirstRunSetup(actor domain.User) bool {
	return actor.Role == domain.RoleRoot
}
