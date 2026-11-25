package auth

import "aagasa/internal/domain"

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

func IsValidRole(role domain.UserRole) bool {
	return Authority(role) > 0
}

func AtLeast(actor domain.User, required domain.UserRole) bool {
	return Authority(actor.Role) >= Authority(required)
}

func CanApprovePasses(actor domain.User) bool {
	return AtLeast(actor, domain.RoleAdmin)
}

func CanManageUsers(actor domain.User) bool {
	return AtLeast(actor, domain.RoleAdmin)
}

func CanCreateUserWithRole(actor domain.User, role domain.UserRole) bool {
	if !IsValidRole(role) || role == domain.RoleRoot {
		return false
	}
	if role == domain.RoleAdmin {
		return actor.Role == domain.RoleRoot
	}
	return CanManageUsers(actor)
}

func CanChangeRole(actor, target domain.User, newRole domain.UserRole) bool {
	if actor.Role != domain.RoleRoot {
		return false
	}
	if !IsValidRole(newRole) || newRole == domain.RoleRoot {
		return false
	}
	return target.Role != domain.RoleRoot
}

func CanDeleteUser(actor, target domain.User) bool {
	if actor.Role != domain.RoleRoot {
		return false
	}
	if target.Role == domain.RoleRoot || target.ID == actor.ID {
		return false
	}
	return true
}

func CanConfigureStation(actor domain.User) bool {
	return actor.Role == domain.RoleRoot
}

func CanRunFirstRunSetup(actor domain.User) bool {
	return actor.Role == domain.RoleRoot
}
