package authz

// legacyPolicyEffect normalizes persisted effects and scopes without deciding
// where permissions are read. Unknown effects remain invalid; callers choose
// load failure (snapshot) or denial (current primary-database authorization).
func legacyPolicyEffect(effect, scope, extra string) (normalized string, valid bool) {
	if effect == "" {
		effect = EffectAllow
	}
	if effect != EffectAllow && effect != EffectDeny {
		return EffectDeny, false
	}
	if extra != "" || (scope != "" && scope != "all") {
		return EffectDeny, true
	}
	return effect, true
}
