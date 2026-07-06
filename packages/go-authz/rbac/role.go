package rbac

import "strings"

type Role string

const (
	RoleCustomer Role = "CUSTOMER"
	RoleProvider Role = "PROVIDER"
	RoleAdmin    Role = "ADMIN"

	RoleSeller   Role = RoleProvider
	RoleOperator Role = RoleAdmin
)

func Canonical(role string) (Role, bool) {
	switch strings.ToUpper(strings.TrimSpace(role)) {
	case string(RoleCustomer):
		return RoleCustomer, true
	case string(RoleProvider), "SELLER":
		return RoleProvider, true
	case string(RoleAdmin), "OPERATOR":
		return RoleAdmin, true
	default:
		return "", false
	}
}
