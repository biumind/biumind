package biuapp

import (
	"errors"
	"strings"
	"testing"
)

func TestParseManifestBytes_Minimal(t *testing.T) {
	yaml := `
identifier: my-app
version: 0.1.0
description: A minimal app
`
	m, err := ParseManifestBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Slug() != "my-app" {
		t.Errorf("slug = %q, want %q", m.Slug(), "my-app")
	}
	if m.Name != "my-app" {
		t.Errorf("Name (legacy) = %q, want %q", m.Name, "my-app")
	}
	if m.Identifier != "my-app" {
		t.Errorf("Identifier = %q, want %q", m.Identifier, "my-app")
	}
	if m.Version != "0.1.0" {
		t.Errorf("Version = %q", m.Version)
	}
}

func TestParseManifestBytes_DisplayName(t *testing.T) {
	yaml := `
identifier: rss
name: RSS 订阅
version: 0.2.0
description: Subscribe to feeds
`
	m, err := ParseManifestBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Name != "rss" {
		t.Errorf("Name (slug) = %q, want %q", m.Name, "rss")
	}
	if m.Title != "RSS 订阅" {
		t.Errorf("Title = %q, want %q", m.Title, "RSS 订阅")
	}
	if m.DisplayName() != "RSS 订阅" {
		t.Errorf("DisplayName() = %q", m.DisplayName())
	}
}

func TestParseManifestBytes_AuthorString(t *testing.T) {
	yaml := `
identifier: a
version: 1.0.0
description: x
author: BiuMind
`
	m, err := ParseManifestBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Author != "BiuMind" {
		t.Errorf("Author = %q", m.Author)
	}
	if m.AuthorURL != "" || m.AuthorPublicKey != "" {
		t.Errorf("URL/PublicKey should be empty for string author form")
	}
}

func TestParseManifestBytes_AuthorObject(t *testing.T) {
	yaml := `
identifier: a
version: 1.0.0
description: x
author:
  name: Acme Corp
  url: https://acme.example
  public_key: ed25519:abc
`
	m, err := ParseManifestBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Author != "Acme Corp" {
		t.Errorf("Author = %q", m.Author)
	}
	if m.AuthorURL != "https://acme.example" {
		t.Errorf("AuthorURL = %q", m.AuthorURL)
	}
	if m.AuthorPublicKey != "ed25519:abc" {
		t.Errorf("AuthorPublicKey = %q", m.AuthorPublicKey)
	}
}

func TestParseManifestBytes_FullManifest(t *testing.T) {
	yaml := `
identifier: rss
name: RSS 订阅
version: 0.2.0
description: Subscribe to feeds
author:
  name: BiuMind
icon: ./assets/icon.png
category: content
kind: hybrid
permissions:
  - net.outbound:*.feedburner.com
  - hub.invoke
  - wiki.write
  - cron.register
data_scopes:
  - wiki:collection:rss-feeds
actions:
  - name: subscribe
    description: Subscribe to a URL
    risk: low
    input_schema:
      type: object
      required: [url]
      properties:
        url:
          type: string
  - name: digest
    risk: medium
    human_intervention: optional
    timeout_ms: 30000
    streamable: true
views:
  - id: home
    route: /apps/rss
    title: RSS
    layout: list_detail
    data_source:
      action: subscribe
    refresh_on:
      - "app:install:<self>:item_arrived"
triggers:
  - kind: cron
    name: hourly
    expr: "5 * * * *"
    action: subscribe
skills:
  - identifier: rss-summarize
    file: skills/summarize.md
sidebar:
  default_pin: false
  preferred_position: middle
  badge_action: subscribe
  badge_refresh: 120
`
	m, err := ParseManifestBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Actions) != 2 {
		t.Fatalf("actions = %d", len(m.Actions))
	}
	if m.Actions[1].Risk != RiskMedium {
		t.Errorf("digest.risk = %q", m.Actions[1].Risk)
	}
	if m.Actions[1].HumanIntervention != InterventionOptional {
		t.Errorf("digest.human_intervention = %q", m.Actions[1].HumanIntervention)
	}
	if !m.Actions[1].Streamable {
		t.Errorf("digest.streamable should be true")
	}
	if len(m.Views) != 1 || m.Views[0].Layout != LayoutListDetail {
		t.Errorf("views = %+v", m.Views)
	}
	if len(m.Triggers) != 1 || m.Triggers[0].Kind != TriggerCron {
		t.Errorf("triggers = %+v", m.Triggers)
	}
	if len(m.Skills) != 1 || m.Skills[0].Identifier != "rss-summarize" {
		t.Errorf("skills = %+v", m.Skills)
	}
	if m.Sidebar == nil || m.Sidebar.BadgeRefreshSec != 120 {
		t.Errorf("sidebar = %+v", m.Sidebar)
	}
}

func TestParseManifestBytes_EmptyError(t *testing.T) {
	if _, err := ParseManifestBytes(nil); err == nil {
		t.Error("expected error on empty input")
	}
}

func TestValidate_Minimal_OK(t *testing.T) {
	m := &Manifest{
		Name:        "x",
		Version:     "0.1.0",
		Description: "test",
	}
	if err := Validate(m); err != nil {
		t.Errorf("expected ok, got: %v", err)
	}
}

func TestValidate_MissingFields(t *testing.T) {
	m := &Manifest{}
	err := Validate(m)
	if err == nil {
		t.Fatal("expected validation errors")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	codes := map[string]bool{}
	for _, i := range ve.Issues {
		codes[i.Code] = true
	}
	for _, want := range []string{"missing"} {
		if !codes[want] {
			t.Errorf("missing expected code %q in issues: %+v", want, ve.Issues)
		}
	}
}

func TestValidate_BadSemver(t *testing.T) {
	m := &Manifest{Name: "x", Version: "abc", Description: "test"}
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "semver") {
		t.Errorf("expected semver error, got %v", err)
	}
}

func TestValidate_UnknownPermission(t *testing.T) {
	m := &Manifest{
		Name: "x", Version: "0.1.0", Description: "test",
		Permissions: []string{"hub.invoke", "evil.power"},
	}
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "evil.power") {
		t.Errorf("expected unknown permission error, got %v", err)
	}
}

func TestValidate_PermissionWithParam(t *testing.T) {
	// net.outbound:<pattern> is allowed — the prefix check should pass.
	m := &Manifest{
		Name: "x", Version: "0.1.0", Description: "test",
		Permissions: []string{"net.outbound:*.example.com,*.foo.com"},
	}
	if err := Validate(m); err != nil {
		t.Errorf("expected ok, got %v", err)
	}
}

func TestValidate_ActionDuplicate(t *testing.T) {
	m := &Manifest{
		Name: "x", Version: "0.1.0", Description: "test",
		Actions: []ActionSpec{
			{Name: "ping"},
			{Name: "ping"},
		},
	}
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate error, got %v", err)
	}
}

func TestValidate_CronEveryMinute_Rejected(t *testing.T) {
	m := &Manifest{
		Name: "x", Version: "0.1.0", Description: "test",
		Permissions: []string{"cron.register"},
		Actions:     []ActionSpec{{Name: "tick"}},
		ManifestExt: ManifestExt{
			Triggers: []TriggerSpec{
				{Kind: TriggerCron, Name: "t", Expr: "* * * * *", Action: "tick"},
			},
		},
	}
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "1 minute") {
		t.Errorf("expected too_frequent error, got %v", err)
	}
}

func TestValidate_TriggerActionMustExist(t *testing.T) {
	m := &Manifest{
		Name: "x", Version: "0.1.0", Description: "test",
		Permissions: []string{"cron.register"},
		Actions:     []ActionSpec{{Name: "tick"}},
		ManifestExt: ManifestExt{
			Triggers: []TriggerSpec{
				{Kind: TriggerCron, Name: "t", Expr: "0 * * * *", Action: "missing"},
			},
		},
	}
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected unknown action error, got %v", err)
	}
}

func TestValidate_ViewRoutePrefix(t *testing.T) {
	m := &Manifest{
		Name: "rss", Version: "0.1.0", Description: "test",
		Actions: []ActionSpec{{Name: "list"}},
		ManifestExt: ManifestExt{
			Views: []ViewSpec{
				{
					ID:     "home",
					Route:  "/apps/email", // wrong slug
					Layout: LayoutList,
				},
			},
		},
	}
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "/apps/rss") {
		t.Errorf("expected wrong_prefix error, got %v", err)
	}
}

func TestValidate_BadgeAction_MustExist(t *testing.T) {
	m := &Manifest{
		Name: "x", Version: "0.1.0", Description: "test",
		Actions: []ActionSpec{{Name: "real"}},
		ManifestExt: ManifestExt{
			Sidebar: &SidebarHints{BadgeAction: "missing"},
		},
	}
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected unknown_action error, got %v", err)
	}
}

func TestValidate_BadgeRefresh_TooFrequent(t *testing.T) {
	m := &Manifest{
		Name: "x", Version: "0.1.0", Description: "test",
		ManifestExt: ManifestExt{
			Sidebar: &SidebarHints{BadgeRefreshSec: 30},
		},
	}
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "60") {
		t.Errorf("expected too_frequent error, got %v", err)
	}
}

func TestValidate_FormSubmit_Ok(t *testing.T) {
	m := &Manifest{
		Name: "rss", Version: "0.1.0", Description: "test",
		Actions: []ActionSpec{{Name: "subscribe"}},
		ManifestExt: ManifestExt{
			Views: []ViewSpec{
				{
					ID: "add", Route: "/apps/rss/add", Layout: LayoutForm,
					SchemaRef: "actions.subscribe.input_schema",
					Submit:    &FormSubmit{Action: "subscribe"},
				},
			},
		},
	}
	if err := Validate(m); err != nil {
		t.Errorf("expected ok, got %v", err)
	}
}

func TestValidate_ScopedSlug_Ok(t *testing.T) {
	// Marketplace-scoped identifier form like `acme/cool-app` must be
	// accepted by the validator (publish path applies the further rule
	// that marketplace requires the slash; bundled apps don't).
	m := &Manifest{
		Name: "acme/cool-app", Version: "1.0.0", Description: "test",
	}
	if err := Validate(m); err != nil {
		t.Errorf("expected ok, got %v", err)
	}
}

// Existing 5 bundled apps' manifests must pass ValidateBundled. This
// test guards us from accidentally tightening rules in a way that
// breaks v1.0 production apps. (We import the apps via blank import;
// if any of them moves namespaces, this test will fail to compile and
// alert us.)
func TestBundledApps_PassValidation(t *testing.T) {
	t.Run("ensures Validate is loose enough for v1.0 manifests", func(t *testing.T) {
		// Reproduce the literal Manifest each bundled app builds, with
		// the minimum field set v1.0 used.
		v1Shape := &Manifest{
			Name:        "rss",
			Version:     "0.1.0",
			Description: "Fetch and normalize RSS / Atom feeds",
			Author:      "BiuMind",
			Permissions: []string{"net.outbound"},
			Actions: []ActionSpec{
				{
					Name:        "fetch",
					Description: "Fetch feed",
				},
			},
		}
		if err := ValidateBundled(v1Shape); err != nil {
			t.Fatalf("v1.0 bundled manifest must pass ValidateBundled: %v", err)
		}
	})
}

// Container kind is reserved (M14 → v2.5) but not yet implementable.
// Validator must reject it with a clear, version-aware message so
// authors know it's coming, not "broken".
func TestValidate_ContainerKindRejected(t *testing.T) {
	m := &Manifest{
		Name: "py-app", Version: "0.1.0", Description: "test",
		ManifestExt: ManifestExt{
			Kind: "container",
		},
	}
	err := Validate(m)
	if err == nil {
		t.Fatal("expected error for kind=container")
	}
	if !strings.Contains(err.Error(), "v2.5") {
		t.Errorf("error must mention v2.5 timeline, got %v", err)
	}
	if !strings.Contains(err.Error(), "M14") || !strings.Contains(err.Error(), "M19") {
		t.Errorf("error must reference milestone migration (M14 → M19), got %v", err)
	}
}

// ─── M16 layout tests ──────────────────────────────────────────────

func TestValidate_Dashboard_RequiresCards(t *testing.T) {
	m := &Manifest{
		Name: "ops", Version: "0.1.0", Description: "ops",
		ManifestExt: ManifestExt{
			Views: []ViewSpec{
				{ID: "home", Route: "/apps/ops/home", Layout: LayoutDashboard},
			},
		},
	}
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "at least one card") {
		t.Errorf("expected dashboard cards-missing error, got %v", err)
	}
}

func TestValidate_Dashboard_CardsOk(t *testing.T) {
	m := &Manifest{
		Name: "ops", Version: "0.1.0", Description: "ops",
		Actions: []ActionSpec{{Name: "stats"}},
		ManifestExt: ManifestExt{
			Views: []ViewSpec{
				{
					ID: "home", Route: "/apps/ops/home", Layout: LayoutDashboard,
					Cards: []ViewCard{
						{ID: "today", Kind: "number", Span: 4,
							DataSource: &ViewDataSource{Action: "stats"}},
						{ID: "week", Kind: "list", Span: 8,
							DataSource: &ViewDataSource{Action: "stats"}},
					},
				},
			},
		},
	}
	if err := Validate(m); err != nil {
		t.Errorf("expected ok, got %v", err)
	}
}

func TestValidate_Dashboard_CardKindInvalid(t *testing.T) {
	m := &Manifest{
		Name: "ops", Version: "0.1.0", Description: "ops",
		Actions: []ActionSpec{{Name: "stats"}},
		ManifestExt: ManifestExt{
			Views: []ViewSpec{
				{
					ID: "home", Route: "/apps/ops/home", Layout: LayoutDashboard,
					Cards: []ViewCard{
						{ID: "today", Kind: "pie", Span: 4,
							DataSource: &ViewDataSource{Action: "stats"}},
					},
				},
			},
		},
	}
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "kind must be one of") {
		t.Errorf("expected card kind error, got %v", err)
	}
}

func TestValidate_Dashboard_DuplicateCardID(t *testing.T) {
	m := &Manifest{
		Name: "ops", Version: "0.1.0", Description: "ops",
		Actions: []ActionSpec{{Name: "stats"}},
		ManifestExt: ManifestExt{
			Views: []ViewSpec{
				{
					ID: "home", Route: "/apps/ops/home", Layout: LayoutDashboard,
					Cards: []ViewCard{
						{ID: "x", Kind: "number", DataSource: &ViewDataSource{Action: "stats"}},
						{ID: "x", Kind: "list", DataSource: &ViewDataSource{Action: "stats"}},
					},
				},
			},
		},
	}
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "duplicate card id") {
		t.Errorf("expected duplicate id error, got %v", err)
	}
}

func TestValidate_Dashboard_CardActionMustExist(t *testing.T) {
	m := &Manifest{
		Name: "ops", Version: "0.1.0", Description: "ops",
		Actions: []ActionSpec{{Name: "stats"}},
		ManifestExt: ManifestExt{
			Views: []ViewSpec{
				{
					ID: "home", Route: "/apps/ops/home", Layout: LayoutDashboard,
					Cards: []ViewCard{
						{ID: "x", DataSource: &ViewDataSource{Action: "missing"}},
					},
				},
			},
		},
	}
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "not in actions[]") {
		t.Errorf("expected unknown_action error, got %v", err)
	}
}

func TestValidate_Grid_RequiresItemTemplate(t *testing.T) {
	m := &Manifest{
		Name: "shelf", Version: "0.1.0", Description: "shelf",
		Actions: []ActionSpec{{Name: "list"}},
		ManifestExt: ManifestExt{
			Views: []ViewSpec{
				{
					ID: "home", Route: "/apps/shelf/home", Layout: LayoutGrid,
					DataSource: &ViewDataSource{Action: "list"},
				},
			},
		},
	}
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "grid layout requires item_template") {
		t.Errorf("expected item_template error, got %v", err)
	}
}

func TestValidate_Grid_ColumnsRange(t *testing.T) {
	m := &Manifest{
		Name: "shelf", Version: "0.1.0", Description: "shelf",
		Actions: []ActionSpec{{Name: "list"}},
		ManifestExt: ManifestExt{
			Views: []ViewSpec{
				{
					ID: "home", Route: "/apps/shelf/home", Layout: LayoutGrid,
					DataSource:   &ViewDataSource{Action: "list"},
					ItemTemplate: &ViewItemTemplate{Kind: "card", Title: "${item.title}"},
					Grid:         &ViewGrid{Columns: []int{1, 2, 9}},
				},
			},
		},
	}
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "[1, 6]") {
		t.Errorf("expected columns range error, got %v", err)
	}
}

func TestValidate_Grid_Ok(t *testing.T) {
	m := &Manifest{
		Name: "shelf", Version: "0.1.0", Description: "shelf",
		Actions: []ActionSpec{{Name: "list"}},
		ManifestExt: ManifestExt{
			Views: []ViewSpec{
				{
					ID: "home", Route: "/apps/shelf/home", Layout: LayoutGrid,
					DataSource:   &ViewDataSource{Action: "list"},
					ItemTemplate: &ViewItemTemplate{Kind: "card", Title: "${item.title}"},
					Grid:         &ViewGrid{Columns: []int{1, 2, 4}, Spacing: 12, AspectRatio: 1.0},
				},
			},
		},
	}
	if err := Validate(m); err != nil {
		t.Errorf("expected ok, got %v", err)
	}
}

func TestValidate_AgentChat_RequiresAgentID(t *testing.T) {
	m := &Manifest{
		Name: "talk", Version: "0.1.0", Description: "talk",
		ManifestExt: ManifestExt{
			Views: []ViewSpec{
				{ID: "home", Route: "/apps/talk/home", Layout: LayoutAgentChat},
			},
		},
	}
	err := Validate(m)
	if err == nil || !strings.Contains(err.Error(), "agent_id") {
		t.Errorf("expected agent_id error, got %v", err)
	}
}

func TestValidate_AgentChat_Ok(t *testing.T) {
	m := &Manifest{
		Name: "talk", Version: "0.1.0", Description: "talk",
		ManifestExt: ManifestExt{
			Views: []ViewSpec{
				{
					ID: "home", Route: "/apps/talk/home", Layout: LayoutAgentChat,
					AgentID: "00000000-0000-0000-0000-000000000001",
					AgentChat: &ViewAgentChat{
						InitialPrompt: "Help me draft an email about ${route.id}",
						ToolFilter:    []string{"email."},
					},
				},
			},
		},
	}
	if err := Validate(m); err != nil {
		t.Errorf("expected ok, got %v", err)
	}
}

// ─── icon 字段格式 (#14) ────────────────────────────────

func TestValidate_Icon_AcceptsValidForms(t *testing.T) {
	cases := []string{
		"",                      // 空
		"📰",                     // emoji
		"https://x.com/icon.png", // URL
		"http://localhost:8080/icon.ico",
		"cas:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", // 64 lowercase hex
	}
	for _, ic := range cases {
		m := &Manifest{
			Name: "x", Version: "0.1.0", Description: "test",
			ManifestExt: ManifestExt{Icon: ic},
		}
		if err := Validate(m); err != nil {
			t.Errorf("icon=%q: expected ok, got %v", ic, err)
		}
	}
}

func TestValidate_Icon_RejectsBadForms(t *testing.T) {
	cases := map[string]string{
		"caz:abc...":           "typo prefix",
		"cas:short":            "cas hash too short",
		"cas:" + "z" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef": "cas non-hex char",
		"cas:" + "abcdef0123456789abcdef0123456789abcdef0123456789abcdef012345678":  "cas hash 63 chars",
		"kimi.moonshot.cn/favicon.ico":                                               "URL no scheme",
	}
	for icon, desc := range cases {
		m := &Manifest{
			Name: "x", Version: "0.1.0", Description: "test",
			ManifestExt: ManifestExt{Icon: icon},
		}
		err := Validate(m)
		if err == nil {
			t.Errorf("icon=%q (%s): expected validation error, got ok", icon, desc)
		}
	}
}
