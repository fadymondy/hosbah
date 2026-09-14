package tenancy

import (
	"context"

	"github.com/togo-framework/authz"
)

// Team roles (team_members.role). Platform administration is separate: it is the
// `admin` role on the user account, not a team role.
const (
	RoleOwner    = "owner"
	RoleAdmin    = "admin"
	RoleDeployer = "deployer"
	RoleViewer   = "viewer"
)

// Membership is the caller's standing in one team — the resource authz policies judge.
type Membership struct {
	TeamID string
	UserID string
	Role   string
}

// Abilities a membership can hold. Anything not listed here is denied (authz is default-deny).
const (
	AbilityView          = "view"           // see the team, its members, its resources
	AbilityDeploy        = "deploy"         // deploy / roll back / restart apps (P2)
	AbilityManageTeam    = "manage_team"    // rename, settings
	AbilityManageMembers = "manage_members" // invite, change roles, remove
	AbilityManageTokens  = "manage_tokens"  // mint and revoke team MCP tokens
	AbilityViewAudit     = "view_audit"     // read the team's audit log
	AbilityOperate       = "operate"        // run read-only server operations (system.info, docker.check)
	AbilityManageServers = "manage_servers" // add/remove servers, provisioning steps, agent updates, provider credentials
	AbilityRevealSecrets = "reveal_secrets" // reveal a sealed secret such as a provider credential (audited)
)

func roleIn(roles ...string) authz.PolicyFunc {
	return func(_ context.Context, _ any, resource any) (bool, error) {
		m, ok := resource.(*Membership)
		if !ok || m == nil {
			return false, nil
		}
		for _, r := range roles {
			if m.Role == r {
				return true, nil
			}
		}
		return false, nil
	}
}

func init() {
	authz.Policy(authz.TypeName(&Membership{}), map[string]authz.PolicyFunc{
		AbilityView:          roleIn(RoleOwner, RoleAdmin, RoleDeployer, RoleViewer),
		AbilityDeploy:        roleIn(RoleOwner, RoleAdmin, RoleDeployer),
		AbilityManageTeam:    roleIn(RoleOwner, RoleAdmin),
		AbilityManageMembers: roleIn(RoleOwner, RoleAdmin),
		AbilityManageTokens:  roleIn(RoleOwner, RoleAdmin),
		AbilityViewAudit:     roleIn(RoleOwner, RoleAdmin),
		AbilityOperate:       roleIn(RoleOwner, RoleAdmin, RoleDeployer),
		AbilityManageServers: roleIn(RoleOwner, RoleAdmin),
		AbilityRevealSecrets: roleIn(RoleOwner, RoleAdmin),
	})
}

// Can reports whether a membership holds an ability. Errors deny.
func Can(ctx context.Context, m *Membership, ability string) bool {
	if m == nil {
		return false
	}
	ok, err := authz.Can(ctx, m.UserID, ability, m)
	return err == nil && ok
}

// ValidRole reports whether r is a team role.
func ValidRole(r string) bool {
	switch r {
	case RoleOwner, RoleAdmin, RoleDeployer, RoleViewer:
		return true
	}
	return false
}
