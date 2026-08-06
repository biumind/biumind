// PermissionUpdate dispatcher — single entry point for changing a
// runtime Context based on a wire-format sdkproto.PermissionUpdate.
//
// The Context is mutex-guarded in place, so we mutate and rely on
// observers (sandbox, system prompt) to react.
//
// Persistence is intentionally NOT done here. Callers (slash command,
// settings loader) decide whether to also write the change back to
// settings.json via package settings's Persist* helpers.

package permissions

import (
	"fmt"

	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

// SourceFromDestination maps the sdkproto destination string used in
// PermissionUpdate values onto the in-memory Source label the Context
// rule/dir bookkeeping uses. Unknown destinations fall back to
// SrcSession because the safest default for an unrecognised hint is
// "ephemeral" — never silently promote to a persistent layer.
func SourceFromDestination(dest string) Source {
	switch dest {
	case sdkproto.PermissionDestSession:
		return SrcSession
	case sdkproto.PermissionDestLocalSettings:
		return SrcLocalSettings
	case sdkproto.PermissionDestUserSettings:
		return SrcUserSettings
	case sdkproto.PermissionDestProjectSettings:
		return SrcProjectSettings
	case sdkproto.PermissionDestCliArg:
		return SrcCLIArg
	}
	return SrcSession
}

// ApplyPermissionUpdate folds one wire-format update into ctx. Returns
// a non-nil error only on schema-level invalidity (nil ctx, nil
// update, unknown variant). A successful apply for AddDirectories /
// RemoveDirectories also fires the dir-change observers registered on
// ctx.
//
// Rule-style updates (AddRules / ReplaceRules / RemoveRules) and
// SetModeUpdate are handled too so the dispatcher can serve as the
// single ingestion point for any future wire-format permission change.
func ApplyPermissionUpdate(ctx *Context, u sdkproto.PermissionUpdate) error {
	if ctx == nil {
		return fmt.Errorf("permissions.ApplyPermissionUpdate: nil context")
	}
	if u == nil {
		return fmt.Errorf("permissions.ApplyPermissionUpdate: nil update")
	}

	switch v := u.(type) {
	case *sdkproto.AddDirectories:
		ctx.AddDirectories(SourceFromDestination(v.Destination), v.Directories)
		return nil

	case *sdkproto.RemoveDirectories:
		ctx.RemoveDirectories(v.Directories)
		return nil

	case *sdkproto.AddRules:
		src := SourceFromDestination(v.Destination)
		behavior := behaviorFromString(v.Behavior)
		ctx.AddRules(src, behavior, ruleStrings(v.Rules))
		return nil

	case *sdkproto.ReplaceRules:
		src := SourceFromDestination(v.Destination)
		behavior := behaviorFromString(v.Behavior)
		ctx.ReplaceRules(src, behavior, ruleStrings(v.Rules))
		return nil

	case *sdkproto.RemoveRules:
		// Context has no removeRules primitive; emulate by re-replacing
		// after filtering. Cost is small (rule lists are tiny). We do
		// this here rather than in Context so the public API stays
		// minimal — RemoveRules via wire format is rare (slash UI only).
		src := SourceFromDestination(v.Destination)
		behavior := behaviorFromString(v.Behavior)
		removeSet := make(map[string]struct{}, len(v.Rules))
		for _, r := range ruleStrings(v.Rules) {
			removeSet[r] = struct{}{}
		}
		current := currentRuleStrings(ctx, src, behavior)
		filtered := current[:0]
		for _, r := range current {
			if _, drop := removeSet[r]; drop {
				continue
			}
			filtered = append(filtered, r)
		}
		ctx.ReplaceRules(src, behavior, filtered)
		return nil

	case *sdkproto.SetModeUpdate:
		ctx.SetMode(ModeFromString(v.Mode))
		return nil
	}

	return fmt.Errorf("permissions.ApplyPermissionUpdate: unknown update type %q",
		u.PermissionUpdateType())
}

// ApplyPermissionUpdates is the convenience batch form. Stops at the
// first error so partial application is impossible — callers either
// get every update applied or get a clean failure to surface.
func ApplyPermissionUpdates(ctx *Context, updates []sdkproto.PermissionUpdate) error {
	for i, u := range updates {
		if err := ApplyPermissionUpdate(ctx, u); err != nil {
			return fmt.Errorf("permissions.ApplyPermissionUpdates[%d]: %w", i, err)
		}
	}
	return nil
}

// ─── helpers ──────────────────────────────────────────

func ruleStrings(values []sdkproto.PermissionRuleValue) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		rv := RuleValue{ToolName: v.ToolName, RuleContent: v.RuleContent}
		out = append(out, rv.String())
	}
	return out
}

func behaviorFromString(s string) Behavior {
	switch s {
	case sdkproto.PermissionAllow:
		return BehaviorAllow
	case sdkproto.PermissionDeny:
		return BehaviorDeny
	case sdkproto.PermissionAsk:
		return BehaviorAsk
	}
	return BehaviorAsk
}

func currentRuleStrings(ctx *Context, src Source, behavior Behavior) []string {
	rules := ctx.AllRules(behavior)
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		if r.Source != src {
			continue
		}
		out = append(out, r.Value.String())
	}
	return out
}
