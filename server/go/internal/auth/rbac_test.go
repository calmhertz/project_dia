package auth

import (
	"testing"

	"github.com/google/uuid"

	"aagasa/internal/domain"
)

func userWith(role domain.UserRole) domain.User {
	return domain.User{ID: uuid.New(), Role: role}
}

func TestAuthorityOrdering(t *testing.T) {
	root := Authority(domain.RoleRoot)
	admin := Authority(domain.RoleAdmin)
	normal := Authority(domain.RoleUser)

	if !(root > admin && admin > normal && normal > 0) {
		t.Errorf("authority order wrong: root=%d admin=%d user=%d", root, admin, normal)
	}
	if Authority(domain.UserRole("superuser")) != 0 {
		t.Error("an unknown role must carry no authority")
	}
}

// A normal user must not reach administrative operations.
func TestNormalUserCannotAdminister(t *testing.T) {
	user := userWith(domain.RoleUser)

	if CanManageUsers(user) {
		t.Error("a normal user can manage users")
	}
	if CanApprovePasses(user) {
		t.Error("a normal user can approve passes")
	}
	if CanConfigureStation(user) {
		t.Error("a normal user can configure the station")
	}
	if CanRunFirstRunSetup(user) {
		t.Error("a normal user can run first-run setup")
	}
}

// Admin sits below Root and must not reach Root-only operations.
func TestAdminCannotPerformRootOnlyOperations(t *testing.T) {
	admin := userWith(domain.RoleAdmin)
	target := userWith(domain.RoleUser)

	if CanConfigureStation(admin) {
		t.Error("an admin can configure the station")
	}
	if CanRunFirstRunSetup(admin) {
		t.Error("an admin can run first-run setup")
	}
	if CanChangeRole(admin, target, domain.RoleAdmin) {
		t.Error("an admin can promote users")
	}
	if CanDeleteUser(admin, target) {
		t.Error("an admin can delete users")
	}
	if CanCreateUserWithRole(admin, domain.RoleAdmin) {
		t.Error("an admin can create other admins")
	}
	// An admin may still do its own job.
	if !CanApprovePasses(admin) {
		t.Error("an admin cannot approve passes")
	}
	if !CanCreateUserWithRole(admin, domain.RoleUser) {
		t.Error("an admin cannot create normal users")
	}
}

// spec.md section 17.1: Root cannot be demoted or deleted, by anyone.
func TestRootCannotBeDemotedOrDeleted(t *testing.T) {
	root := userWith(domain.RoleRoot)
	otherRoot := userWith(domain.RoleRoot)

	for _, actor := range []domain.User{root, userWith(domain.RoleAdmin), userWith(domain.RoleUser)} {
		if CanChangeRole(actor, otherRoot, domain.RoleAdmin) {
			t.Errorf("%s can demote root", actor.Role)
		}
		if CanChangeRole(actor, otherRoot, domain.RoleUser) {
			t.Errorf("%s can demote root to user", actor.Role)
		}
		if CanDeleteUser(actor, otherRoot) {
			t.Errorf("%s can delete root", actor.Role)
		}
	}
}

// There is exactly one Root; nobody may mint a second.
func TestNobodyCanCreateOrPromoteToRoot(t *testing.T) {
	root := userWith(domain.RoleRoot)
	target := userWith(domain.RoleUser)

	if CanCreateUserWithRole(root, domain.RoleRoot) {
		t.Error("root can create a second root")
	}
	if CanChangeRole(root, target, domain.RoleRoot) {
		t.Error("root can promote a user to root")
	}
}

func TestRootCanPromoteAndDemoteAdmins(t *testing.T) {
	root := userWith(domain.RoleRoot)
	user := userWith(domain.RoleUser)
	admin := userWith(domain.RoleAdmin)

	if !CanChangeRole(root, user, domain.RoleAdmin) {
		t.Error("root cannot promote a user to admin")
	}
	if !CanChangeRole(root, admin, domain.RoleUser) {
		t.Error("root cannot demote an admin")
	}
	if !CanCreateUserWithRole(root, domain.RoleAdmin) {
		t.Error("root cannot create an admin")
	}
	if !CanDeleteUser(root, admin) {
		t.Error("root cannot delete an admin")
	}
}

func TestRootCannotDeleteItself(t *testing.T) {
	root := userWith(domain.RoleRoot)
	if CanDeleteUser(root, root) {
		t.Error("root can delete itself")
	}
}

func TestUnknownRoleIsRejectedEverywhere(t *testing.T) {
	root := userWith(domain.RoleRoot)
	target := userWith(domain.RoleUser)
	bogus := domain.UserRole("superuser")

	if CanCreateUserWithRole(root, bogus) {
		t.Error("an unknown role can be created")
	}
	if CanChangeRole(root, target, bogus) {
		t.Error("a user can be moved to an unknown role")
	}
	if IsValidRole(bogus) {
		t.Error("an unknown role is reported valid")
	}
}
