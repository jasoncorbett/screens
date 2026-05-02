package views

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jasoncorbett/screens/internal/auth"
	"github.com/jasoncorbett/screens/internal/themes"
)

// validThemeForm returns a url.Values populated with a complete, valid set of
// theme form fields. Tests that want to exercise a single invalid field can
// override that field after calling this helper.
func validThemeForm(name string) url.Values {
	form := url.Values{}
	form.Set("name", name)
	form.Set("color_bg", "#ffffff")
	form.Set("color_surface", "#f5f5f5")
	form.Set("color_border", "#dcdcdc")
	form.Set("color_text", "#111111")
	form.Set("color_text_muted", "#555555")
	form.Set("color_accent", "#7b93ff")
	form.Set("font_family", "system-ui")
	form.Set("radius", "10px")
	return form
}

// adminContext returns a context populated with an admin user and a real
// session (with CSRF token) suitable for handler tests that invoke the bare
// handler directly (i.e., without going through the middleware chain).
func adminContext(t *testing.T, deps *Deps, admin *auth.User) context.Context {
	t.Helper()
	session := createTestSession(t, deps.Auth, admin.ID)
	ctx := auth.ContextWithUser(context.Background(), admin)
	ctx = auth.ContextWithSession(ctx, session)
	return ctx
}

func TestHandleThemeList_RendersThemes(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	// Create a non-default theme so the list contains both default and custom.
	if _, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "kitchen",
		ColorBg:        "#101010",
		ColorSurface:   "#202020",
		ColorBorder:    "#303030",
		ColorText:      "#dddddd",
		ColorTextMuted: "#888888",
		ColorAccent:    "#ff8800",
		FontFamily:     "sans-serif",
		Radius:         "8px",
	}); err != nil {
		t.Fatalf("create kitchen theme: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/themes", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleThemeList(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want to contain text/html", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "default") {
		t.Error("body missing the literal 'default' marker for the default theme")
	}
	if !strings.Contains(body, "kitchen") {
		t.Error("body missing 'kitchen' theme")
	}
	if !strings.Contains(body, "Theme Management") {
		t.Error("body missing page title")
	}
}

func TestHandleThemeList_NoUserContext_Returns403(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/themes", nil)
	rr := httptest.NewRecorder()
	handleThemeList(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestHandleThemeCreate_HappyPath(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	form := validThemeForm("kitchen-day")
	req := httptest.NewRequest(http.MethodPost, "/admin/themes", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleThemeCreate(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/themes?msg=created" {
		t.Errorf("Location = %q, want /admin/themes?msg=created", loc)
	}

	list, err := deps.Themes.List(context.Background())
	if err != nil {
		t.Fatalf("list themes: %v", err)
	}
	var found *themes.Theme
	for i, th := range list {
		if th.Name == "kitchen-day" {
			found = &list[i]
			break
		}
	}
	if found == nil {
		t.Fatal("created theme 'kitchen-day' not found in list")
	}
	if found.ColorBg != "#ffffff" {
		t.Errorf("ColorBg = %q, want %q", found.ColorBg, "#ffffff")
	}
	if found.IsDefault {
		t.Error("freshly created theme should not be the default")
	}
}

func TestHandleThemeCreate_RejectsInvalidFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		field     string
		value     string
		wantField string
	}{
		{name: "empty name", field: "name", value: "", wantField: "name"},
		{name: "whitespace name", field: "name", value: "   ", wantField: "name"},
		{name: "name with markup", field: "name", value: "theme<script>", wantField: "name"},
		{name: "name too long", field: "name", value: strings.Repeat("a", 65), wantField: "name"},
		{name: "invalid color_bg", field: "color_bg", value: "red", wantField: "color_bg"},
		{name: "invalid color_surface", field: "color_surface", value: "not-a-color", wantField: "color_surface"},
		{name: "invalid color_border", field: "color_border", value: "#zz", wantField: "color_border"},
		{name: "invalid color_text", field: "color_text", value: "#1234", wantField: "color_text"},
		{name: "invalid color_text_muted", field: "color_text_muted", value: "rgb(0,0,0)", wantField: "color_text_muted"},
		{name: "invalid color_accent", field: "color_accent", value: "transparent", wantField: "color_accent"},
		{name: "invalid font_family", field: "font_family", value: "Arial; }", wantField: "font_family"},
		{name: "invalid radius", field: "radius", value: "10", wantField: "radius"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deps, q := newTestDeps(t)
			admin := createTestUser(t, q, "admin@example.com", "admin")
			ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

			// Capture the baseline theme count (the seeded default).
			baseline, err := deps.Themes.List(context.Background())
			if err != nil {
				t.Fatalf("baseline list: %v", err)
			}

			form := validThemeForm("valid-name")
			form.Set(tt.field, tt.value)

			req := httptest.NewRequest(http.MethodPost, "/admin/themes", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()

			handleThemeCreate(deps.Themes).ServeHTTP(rr, req)

			// Inline re-render: 200 OK, NOT a redirect.
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (must re-render inline, not redirect)", rr.Code, http.StatusOK)
			}
			body := rr.Body.String()
			// The form should re-render with the rejected value preserved.
			// p.error tag should be present (one of the field error messages).
			if !strings.Contains(body, `class="error"`) {
				t.Error("body missing error message paragraph; expected re-rendered form with errors")
			}

			// No row created.
			after, err := deps.Themes.List(context.Background())
			if err != nil {
				t.Fatalf("after list: %v", err)
			}
			if len(after) != len(baseline) {
				t.Errorf("len(themes) = %d, want %d (no row should be created on validation error)", len(after), len(baseline))
			}
		})
	}
}

func TestHandleThemeCreate_RejectsDuplicateName(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	// Create theme "A" via the service.
	if _, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "A",
		ColorBg:        "#ffffff",
		ColorSurface:   "#f5f5f5",
		ColorBorder:    "#dcdcdc",
		ColorText:      "#111111",
		ColorTextMuted: "#555555",
		ColorAccent:    "#7b93ff",
		FontFamily:     "system-ui",
		Radius:         "10px",
	}); err != nil {
		t.Fatalf("create A: %v", err)
	}

	// Attempt to create another "A" via the handler.
	form := validThemeForm("A")
	req := httptest.NewRequest(http.MethodPost, "/admin/themes", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleThemeCreate(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (duplicate name re-renders inline)", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "already exists") {
		t.Error("body missing 'already exists' message for duplicate name")
	}

	// Only one "A" should still exist.
	list, err := deps.Themes.List(context.Background())
	if err != nil {
		t.Fatalf("list themes: %v", err)
	}
	count := 0
	for _, th := range list {
		if th.Name == "A" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("count of name='A' = %d, want 1", count)
	}
}

func TestHandleThemeEditForm_PrePopulates(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	created, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "lobby-night",
		ColorBg:        "#0a0a0a",
		ColorSurface:   "#141414",
		ColorBorder:    "#222222",
		ColorText:      "#dddddd",
		ColorTextMuted: "#888888",
		ColorAccent:    "#ff8800",
		FontFamily:     "sans-serif",
		Radius:         "8px",
	})
	if err != nil {
		t.Fatalf("create theme: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/themes/"+created.ID+"/edit", nil)
	req.SetPathValue("id", created.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleThemeEditForm(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `value="lobby-night"`) {
		t.Error("body missing value=\"lobby-night\" -- form should pre-populate the existing name")
	}
	if !strings.Contains(body, `value="#0a0a0a"`) {
		t.Error("body missing value=\"#0a0a0a\" -- form should pre-populate the existing color")
	}
}

func TestHandleThemeEditForm_UnknownID(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	req := httptest.NewRequest(http.MethodGet, "/admin/themes/nonexistent/edit", nil)
	req.SetPathValue("id", "nonexistent")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleThemeEditForm(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "error=") {
		t.Errorf("Location = %q, want redirect with error param", loc)
	}
}

func TestHandleThemeUpdate_HappyPath(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	created, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "workshop",
		ColorBg:        "#000000",
		ColorSurface:   "#111111",
		ColorBorder:    "#222222",
		ColorText:      "#eeeeee",
		ColorTextMuted: "#888888",
		ColorAccent:    "#00ff00",
		FontFamily:     "monospace",
		Radius:         "0",
	})
	if err != nil {
		t.Fatalf("create theme: %v", err)
	}

	form := validThemeForm("workshop-renamed")
	form.Set("color_accent", "#ff00ff")

	req := httptest.NewRequest(http.MethodPost, "/admin/themes/"+created.ID, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", created.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleThemeUpdate(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/themes?msg=updated" {
		t.Errorf("Location = %q, want /admin/themes?msg=updated", loc)
	}

	got, err := deps.Themes.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get updated theme: %v", err)
	}
	if got.Name != "workshop-renamed" {
		t.Errorf("Name = %q, want workshop-renamed", got.Name)
	}
	if got.ColorAccent != "#ff00ff" {
		t.Errorf("ColorAccent = %q, want #ff00ff", got.ColorAccent)
	}
}

func TestHandleThemeUpdate_UnknownID(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	form := validThemeForm("nope")
	req := httptest.NewRequest(http.MethodPost, "/admin/themes/nonexistent", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", "nonexistent")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleThemeUpdate(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "error=") {
		t.Errorf("Location = %q, want redirect with error param", loc)
	}
}

func TestHandleThemeUpdate_RejectsValidationError(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	created, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "valid-theme",
		ColorBg:        "#ffffff",
		ColorSurface:   "#f5f5f5",
		ColorBorder:    "#dcdcdc",
		ColorText:      "#111111",
		ColorTextMuted: "#555555",
		ColorAccent:    "#7b93ff",
		FontFamily:     "system-ui",
		Radius:         "10px",
	})
	if err != nil {
		t.Fatalf("create theme: %v", err)
	}

	form := validThemeForm("valid-theme")
	form.Set("color_bg", "red") // invalid

	req := httptest.NewRequest(http.MethodPost, "/admin/themes/"+created.ID, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", created.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleThemeUpdate(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (validation re-renders inline)", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `class="error"`) {
		t.Error("body missing error paragraph for color_bg")
	}
	// The page header should still show the existing theme name (not the
	// rejected form values' name).
	if !strings.Contains(body, "Edit Theme: valid-theme") {
		t.Error("body missing edit page header showing existing theme name")
	}

	// The DB row should NOT have been updated.
	got, err := deps.Themes.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get theme: %v", err)
	}
	if got.ColorBg != "#ffffff" {
		t.Errorf("ColorBg = %q, want #ffffff (the row should not have been mutated)", got.ColorBg)
	}
}

func TestHandleThemeDelete_HappyPath(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	created, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "to-delete",
		ColorBg:        "#ffffff",
		ColorSurface:   "#f5f5f5",
		ColorBorder:    "#dcdcdc",
		ColorText:      "#111111",
		ColorTextMuted: "#555555",
		ColorAccent:    "#7b93ff",
		FontFamily:     "system-ui",
		Radius:         "10px",
	})
	if err != nil {
		t.Fatalf("create theme: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/themes/"+created.ID+"/delete", nil)
	req.SetPathValue("id", created.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleThemeDelete(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/themes?msg=deleted" {
		t.Errorf("Location = %q, want /admin/themes?msg=deleted", loc)
	}

	if _, err := deps.Themes.GetByID(context.Background(), created.ID); !errors.Is(err, themes.ErrThemeNotFound) {
		t.Errorf("GetByID after delete err = %v, want ErrThemeNotFound", err)
	}
}

func TestHandleThemeDelete_DefaultRejected(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	def, err := deps.Themes.GetDefault(context.Background())
	if err != nil {
		t.Fatalf("get default: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/themes/"+def.ID+"/delete", nil)
	req.SetPathValue("id", def.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleThemeDelete(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "error=Cannot+delete+the+default+theme") {
		t.Errorf("Location = %q, want error=Cannot+delete+the+default+theme", loc)
	}

	// Default theme row should still be present.
	if _, err := deps.Themes.GetByID(context.Background(), def.ID); err != nil {
		t.Errorf("default theme should still exist after rejected delete; err = %v", err)
	}
}

func TestHandleThemeSetDefault_HappyPath(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	originalDefault, err := deps.Themes.GetDefault(context.Background())
	if err != nil {
		t.Fatalf("get default: %v", err)
	}

	created, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "challenger",
		ColorBg:        "#ffffff",
		ColorSurface:   "#f5f5f5",
		ColorBorder:    "#dcdcdc",
		ColorText:      "#111111",
		ColorTextMuted: "#555555",
		ColorAccent:    "#7b93ff",
		FontFamily:     "system-ui",
		Radius:         "10px",
	})
	if err != nil {
		t.Fatalf("create challenger: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/themes/"+created.ID+"/set-default", nil)
	req.SetPathValue("id", created.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleThemeSetDefault(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/themes?msg=set_default" {
		t.Errorf("Location = %q, want /admin/themes?msg=set_default", loc)
	}

	gotChallenger, err := deps.Themes.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get challenger: %v", err)
	}
	if !gotChallenger.IsDefault {
		t.Error("challenger should be the new default")
	}
	gotOriginal, err := deps.Themes.GetByID(context.Background(), originalDefault.ID)
	if err != nil {
		t.Fatalf("get original: %v", err)
	}
	if gotOriginal.IsDefault {
		t.Error("original theme should no longer be the default")
	}
}

func TestHandleThemeSetDefault_UnknownID(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	req := httptest.NewRequest(http.MethodPost, "/admin/themes/nonexistent/set-default", nil)
	req.SetPathValue("id", "nonexistent")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleThemeSetDefault(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "error=") {
		t.Errorf("Location = %q, want redirect with error param", loc)
	}
}

// TestHandleThemeList_DoesNotLeakStaleFormValues verifies that after a
// successful create the GET /admin/themes page does NOT pre-fill the create
// form with the previously submitted values. Since the create handler
// 302-redirects on success, the next GET must render an empty form.
func TestHandleThemeList_DoesNotLeakStaleFormValues(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	form := validThemeForm("posted-name")
	createReq := httptest.NewRequest(http.MethodPost, "/admin/themes", strings.NewReader(form.Encode()))
	createReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createReq = createReq.WithContext(ctx)
	createRR := httptest.NewRecorder()
	handleThemeCreate(deps.Themes).ServeHTTP(createRR, createReq)

	if createRR.Code != http.StatusFound {
		t.Fatalf("create status = %d, want %d", createRR.Code, http.StatusFound)
	}

	// Now GET the list page. The "New Theme" form must render with empty
	// inputs; the "posted-name" string should appear in the table row (as
	// the theme name) but NOT inside a value="..." attribute on the new-form
	// inputs.
	listReq := httptest.NewRequest(http.MethodGet, "/admin/themes", nil)
	listReq = listReq.WithContext(ctx)
	listRR := httptest.NewRecorder()
	handleThemeList(deps.Themes).ServeHTTP(listRR, listReq)

	if listRR.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", listRR.Code, http.StatusOK)
	}
	body := listRR.Body.String()
	if strings.Contains(body, `value="posted-name"`) {
		t.Error("list page leaked the previously posted name into a value=\"...\" attribute -- the new-theme form should be empty after a successful create+redirect")
	}
}

// TestThemeRoutes_CSRFRequired verifies the existing CSRF middleware rejects
// a POST to /admin/themes/{id}/delete without a valid _csrf field. Exercises
// the real middleware chain via httptest.NewServer (AC-27).
func TestThemeRoutes_CSRFRequired(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	created, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "doomed",
		ColorBg:        "#ffffff",
		ColorSurface:   "#f5f5f5",
		ColorBorder:    "#dcdcdc",
		ColorText:      "#111111",
		ColorTextMuted: "#555555",
		ColorAccent:    "#7b93ff",
		FontFamily:     "system-ui",
		Radius:         "10px",
	})
	if err != nil {
		t.Fatalf("create theme: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// POST without a _csrf field; CSRF middleware must reject with 403.
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/themes/"+created.ID+"/delete", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST delete: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (CSRF middleware should reject the missing _csrf field)", resp.StatusCode, http.StatusForbidden)
	}

	// The theme row must still exist.
	if _, err := deps.Themes.GetByID(context.Background(), created.ID); err != nil {
		t.Errorf("theme should still exist after CSRF rejection; err = %v", err)
	}
}
