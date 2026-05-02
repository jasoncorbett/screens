package views

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jasoncorbett/screens/internal/auth"
	"github.com/jasoncorbett/screens/internal/themes"
)

// TestThemeMgmt_MemberCannotAccessThemesPage verifies AC-21: a logged-in
// member GETting /admin/themes is rejected with 403 from RequireRole. This
// is exercised through the full middleware chain (httptest.NewServer) so a
// regression that drops the RequireRole(RoleAdmin) wrapping on the
// theme sub-mux would be caught.
func TestThemeMgmt_MemberCannotAccessThemesPage(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	member := createTestUser(t, q, "member@example.com", "member")
	rawToken, err := deps.Auth.CreateSession(context.Background(), member.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
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

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/admin/themes", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/themes: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d (member must be 403 on /admin/themes)", resp.StatusCode, http.StatusForbidden)
	}
}

// TestThemeMgmt_MemberCannotPostCreate verifies a member with a valid CSRF
// token still cannot POST /admin/themes -- the RequireRole check happens
// after RequireCSRF passes.
func TestThemeMgmt_MemberCannotPostCreate(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	member := createTestUser(t, q, "member@example.com", "member")
	rawToken, err := deps.Auth.CreateSession(context.Background(), member.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, session, err := deps.Auth.ValidateSession(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}

	baseline, err := deps.Themes.List(context.Background())
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	form := validThemeForm("evil-theme")
	form.Set("_csrf", session.CSRFToken)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/themes", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /admin/themes: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d (member POST /admin/themes must be 403)", resp.StatusCode, http.StatusForbidden)
	}

	// No theme should have been created.
	after, err := deps.Themes.List(context.Background())
	if err != nil {
		t.Fatalf("after list: %v", err)
	}
	if len(after) != len(baseline) {
		t.Errorf("len(themes) = %d, want %d (member's POST should not have created a theme)", len(after), len(baseline))
	}
}

// TestThemeMgmt_MemberCannotPostDelete verifies a member with valid CSRF
// cannot delete a theme. The role check is the second line of defence after
// CSRF; a regression that lifted role enforcement would let a member delete
// admin-owned themes.
func TestThemeMgmt_MemberCannotPostDelete(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	// Admin sets up a victim theme via the service (no HTTP).
	victim, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "victim",
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
		t.Fatalf("create victim theme: %v", err)
	}

	member := createTestUser(t, q, "member@example.com", "member")
	rawToken, err := deps.Auth.CreateSession(context.Background(), member.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, session, err := deps.Auth.ValidateSession(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	form := url.Values{}
	form.Set("_csrf", session.CSRFToken)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/themes/"+victim.ID+"/delete", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST delete: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d (member must not delete themes)", resp.StatusCode, http.StatusForbidden)
	}

	// Theme row must still exist.
	if _, err := deps.Themes.GetByID(context.Background(), victim.ID); err != nil {
		t.Errorf("theme should still exist after rejected member delete; err = %v", err)
	}
}

// TestThemeMgmt_AnonymousRedirectedToLogin verifies an anonymous request to
// /admin/themes is redirected to /admin/login by RequireAuth before
// RequireRole runs. Catches a chain-order regression where role check would
// run on a nil user and incorrectly emit 403 instead of 302.
func TestThemeMgmt_AnonymousRedirectedToLogin(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDepsWithGoogle(t)

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/admin/themes", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/html")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/themes: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want %d (anonymous HTML nav should redirect, not 403)", resp.StatusCode, http.StatusFound)
	}
	loc := resp.Header.Get("Location")
	if loc != "/admin/login" {
		t.Errorf("Location = %q, want /admin/login", loc)
	}
}

// TestThemeMgmt_CreateWithoutCSRFRejected verifies that POST /admin/themes
// without a _csrf field is rejected by the existing CSRF middleware. This
// complements the developer's TestThemeRoutes_CSRFRequired (which only
// covered the delete route) by extending the same protection to create.
func TestThemeMgmt_CreateWithoutCSRFRejected(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	baseline, err := deps.Themes.List(context.Background())
	if err != nil {
		t.Fatalf("baseline list: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	// No _csrf field at all.
	form := validThemeForm("nope")
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/themes", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST create: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d (POST without _csrf must be rejected)", resp.StatusCode, http.StatusForbidden)
	}

	after, err := deps.Themes.List(context.Background())
	if err != nil {
		t.Fatalf("after list: %v", err)
	}
	if len(after) != len(baseline) {
		t.Errorf("len(themes) = %d, want %d (CSRF-rejected request must not create a theme)", len(after), len(baseline))
	}
}

// TestThemeMgmt_UpdateWithoutCSRFRejected verifies POST /admin/themes/{id}
// requires a valid CSRF token.
func TestThemeMgmt_UpdateWithoutCSRFRejected(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	target, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "stable",
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
		t.Fatalf("create: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	form := validThemeForm("renamed-without-csrf")
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/themes/"+target.ID, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST update: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d (update without _csrf must be rejected)", resp.StatusCode, http.StatusForbidden)
	}

	got, err := deps.Themes.GetByID(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("get target: %v", err)
	}
	if got.Name != "stable" {
		t.Errorf("Name = %q, want stable (theme should not have been renamed)", got.Name)
	}
}

// TestThemeMgmt_SetDefaultWithoutCSRFRejected verifies the set-default route
// is also CSRF-protected. Catches a regression where set-default were wired
// outside the CSRF-wrapped admin sub-mux.
func TestThemeMgmt_SetDefaultWithoutCSRFRejected(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	originalDefault, err := deps.Themes.GetDefault(context.Background())
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	challenger, err := deps.Themes.Create(context.Background(), themes.Input{
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

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/themes/"+challenger.ID+"/set-default", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST set-default: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d (set-default without _csrf must be rejected)", resp.StatusCode, http.StatusForbidden)
	}

	// Original default should still be the default.
	gotOriginal, err := deps.Themes.GetByID(context.Background(), originalDefault.ID)
	if err != nil {
		t.Fatalf("get original: %v", err)
	}
	if !gotOriginal.IsDefault {
		t.Error("original default theme lost its default flag despite CSRF rejection")
	}
	gotChallenger, err := deps.Themes.GetByID(context.Background(), challenger.ID)
	if err != nil {
		t.Fatalf("get challenger: %v", err)
	}
	if gotChallenger.IsDefault {
		t.Error("challenger should not be default after CSRF rejection")
	}
}

// TestThemeMgmt_DeleteWithWrongCSRFRejected verifies that supplying a
// token that does not match the session's CSRFToken is still rejected. A
// constant-time-compare bug or a missing comparison would let an attacker
// forge a token by knowing only that "some token" was needed.
func TestThemeMgmt_DeleteWithWrongCSRFRejected(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	target, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "doomed-but-not-really",
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
		t.Fatalf("create target: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	form := url.Values{}
	form.Set("_csrf", "this-is-not-the-real-token")

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/themes/"+target.ID+"/delete", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST delete: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d (wrong _csrf must be rejected)", resp.StatusCode, http.StatusForbidden)
	}

	if _, err := deps.Themes.GetByID(context.Background(), target.ID); err != nil {
		t.Errorf("theme should still exist after wrong _csrf rejection; err = %v", err)
	}
}

// TestHandleThemeCreate_PreservesRejectedFormValues verifies that when a
// POST has multiple invalid fields, the inline re-rendered form contains
// each rejected value in a value="..." attribute so the user does not have
// to retype anything. This is the user-visible promise of ADR-004.
func TestHandleThemeCreate_PreservesRejectedFormValues(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	form := validThemeForm("good-name")
	form.Set("name", "")                        // invalid: empty
	form.Set("color_bg", "red")                 // invalid: not a hex
	form.Set("radius", "10")                    // invalid: no unit
	form.Set("font_family", "Arial; }<script>") // invalid: contains ; { } < >
	// Set a complex but VALID color_surface so we can assert the unrejected
	// value is still echoed back too.
	form.Set("color_surface", "#abcdef")

	req := httptest.NewRequest(http.MethodPost, "/admin/themes", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleThemeCreate(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (validation should re-render inline)", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()

	// Rejected values must appear in value="..." so the user does not retype.
	wantValues := []string{
		`value="red"`,
		`value="10"`,
		// font_family contains '<' and '>' which templ HTML-escapes to &lt;/&gt;
		// inside an attribute. Look for the safely-escaped form.
		`value="Arial; }&lt;script&gt;"`,
		// Untouched valid value is also echoed back.
		`value="#abcdef"`,
	}
	for _, want := range wantValues {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q -- rejected form values must be preserved on re-render", want)
		}
	}

	// All four rejected fields should produce an inline error paragraph.
	// Count <p class="error"> occurrences.
	errCount := strings.Count(body, `class="error"`)
	if errCount < 4 {
		t.Errorf("found %d error paragraphs, want at least 4 (one per rejected field)", errCount)
	}

	// The empty-name field produces value="" -- verify the input is empty
	// (i.e., we did not silently substitute the form's good-name default).
	// Locate the name input and confirm the value attribute is empty.
	nameIdx := strings.Index(body, `name="name"`)
	if nameIdx < 0 {
		t.Fatal("body missing name=\"name\" input")
	}
	// Look at a window of characters around the name input.
	start := nameIdx
	end := nameIdx + 80
	if end > len(body) {
		end = len(body)
	}
	if !strings.Contains(body[start:end], `value=""`) {
		t.Errorf("name input did not have value=\"\"; got: %q", body[start:end])
	}
}

// TestHandleThemeUpdate_PreservesRejectedFormValuesAndKeepsPageHeader
// verifies the same value-preservation guarantee for the edit form, and
// also asserts the page header shows the EXISTING theme name (not the
// rejected form name) so the user knows which theme they are editing.
func TestHandleThemeUpdate_PreservesRejectedFormValuesAndKeepsPageHeader(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	created, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "original-name",
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
		t.Fatalf("create: %v", err)
	}

	form := validThemeForm("attempted-rename")
	form.Set("color_bg", "not-a-color") // invalid
	form.Set("radius", "")              // invalid: empty
	form.Set("color_accent", "#beef")   // invalid: 4-char hex

	req := httptest.NewRequest(http.MethodPost, "/admin/themes/"+created.ID, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", created.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleThemeUpdate(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (validation must re-render inline)", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()

	// Page header shows the *existing* theme name, not the rejected attempt.
	if !strings.Contains(body, "Edit Theme: original-name") {
		t.Error("body missing 'Edit Theme: original-name' header")
	}
	if strings.Contains(body, "Edit Theme: attempted-rename") {
		t.Error("body wrongly shows the rejected new name in the header")
	}

	// Rejected values are preserved in form inputs so the user can fix them.
	wantValues := []string{
		`value="attempted-rename"`,
		`value="not-a-color"`,
		`value="#beef"`,
	}
	for _, want := range wantValues {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q in the form -- rejected values must be preserved", want)
		}
	}

	// Verify the row was NOT mutated.
	got, err := deps.Themes.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "original-name" {
		t.Errorf("Name = %q, want original-name (validation rejection must not mutate the row)", got.Name)
	}
	if got.ColorBg != "#ffffff" {
		t.Errorf("ColorBg = %q, want #ffffff", got.ColorBg)
	}
}

// TestHandleThemeList_DefaultMarkerByFlagNotByName verifies the "default"
// marker depends on Theme.IsDefault, not on string-matching the name. A
// theme named "default-but-not-really" whose IsDefault is false MUST NOT
// get the marker, even though its name contains the substring "default".
func TestHandleThemeList_DefaultMarkerByFlagNotByName(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	// The seeded default is named "default". Add a non-default theme whose
	// name happens to contain "default" -- it must NOT receive the marker.
	if _, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "default-but-not-really",
		ColorBg:        "#ffffff",
		ColorSurface:   "#f5f5f5",
		ColorBorder:    "#dcdcdc",
		ColorText:      "#111111",
		ColorTextMuted: "#555555",
		ColorAccent:    "#7b93ff",
		FontFamily:     "system-ui",
		Radius:         "10px",
	}); err != nil {
		t.Fatalf("create non-default decoy: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/themes", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleThemeList(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()

	// Exactly one <strong>default</strong> marker should appear (next to
	// the seeded default's row). The <strong> tag is only emitted by the
	// templ when t.IsDefault is true.
	markerCount := strings.Count(body, `<strong>default</strong>`)
	if markerCount != 1 {
		t.Errorf("found %d default markers, want 1 (the marker must reflect IsDefault, not the name)", markerCount)
	}

	// Sanity: both theme names appear in the body.
	if !strings.Contains(body, "default-but-not-really") {
		t.Error("body missing the decoy theme's name")
	}
}

// TestHandleThemeList_ManyThemesRender verifies the list page handles a
// reasonably large number of themes without breaking the HTML structure.
// 50 is well above the spec's <50-row guidance and exercises the table
// loop's templ output.
func TestHandleThemeList_ManyThemesRender(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	for i := 0; i < 50; i++ {
		idxStr := strconv.Itoa(i)
		if _, err := deps.Themes.Create(context.Background(), themes.Input{
			Name:           "theme-" + idxStr,
			ColorBg:        "#ffffff",
			ColorSurface:   "#f5f5f5",
			ColorBorder:    "#dcdcdc",
			ColorText:      "#111111",
			ColorTextMuted: "#555555",
			ColorAccent:    "#7b93ff",
			FontFamily:     "system-ui",
			Radius:         "10px",
		}); err != nil {
			t.Fatalf("create theme-%s: %v", idxStr, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/themes", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleThemeList(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()

	// Spot-check: a low number, a high number, and the seeded default
	// should all be present.
	for _, want := range []string{"theme-0", "theme-49", "default"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// The HTML must close cleanly (no truncation, no <body> mismatch).
	if !strings.Contains(body, "</html>") {
		t.Error("body missing closing </html> -- list output may be truncated")
	}
	// Exactly one default marker for the seeded default.
	markerCount := strings.Count(body, `<strong>default</strong>`)
	if markerCount != 1 {
		t.Errorf("found %d default markers in 51-theme list, want 1", markerCount)
	}
}

// TestHandleThemeEditForm_NameWithSpacesPreserved verifies the edit form
// correctly renders a theme name containing whitelisted characters
// (space, hyphen, underscore) inside value="..." without URL-encoding or
// over-escaping. Spaces are explicitly part of the [A-Za-z0-9 _-] regex.
func TestHandleThemeEditForm_NameWithSpacesPreserved(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	created, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "Kitchen Day _alpha-1",
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
		t.Fatalf("create: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/themes/"+created.ID+"/edit", nil)
	req.SetPathValue("id", created.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleThemeEditForm(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	// Spaces, underscores, hyphens must round-trip into the value="..."
	// attribute exactly as stored. No URL-encoding (%20), no entity encoding
	// (&#32;), no quote stripping.
	want := `value="Kitchen Day _alpha-1"`
	if !strings.Contains(body, want) {
		t.Errorf("body missing %q -- whitelisted name characters must be preserved", want)
	}
	// And the page header.
	if !strings.Contains(body, "Edit Theme: Kitchen Day _alpha-1") {
		t.Error("body missing the page header with the full theme name")
	}
}

// TestHandleThemeUpdate_LongIDDoesNotPanic verifies the update handler
// gracefully handles a 10000-char id (no panic, no DoS, no 500). The id
// flows through GetByID's parameterised query so it is safe at the SQL
// layer; the service returns ErrThemeNotFound; the handler 302s.
func TestHandleThemeUpdate_LongIDDoesNotPanic(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	longID := strings.Repeat("a", 10000)
	form := validThemeForm("any")
	req := httptest.NewRequest(http.MethodPost, "/admin/themes/"+longID, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", longID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleThemeUpdate(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("status = %d, want %d (long id should resolve to 'not found' redirect)", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "error=") {
		t.Errorf("Location = %q, want redirect with error=...", loc)
	}
}

// TestHandleThemeDelete_RedirectErrorIsSanitized verifies that when the
// service returns a sentinel error, the redirect URL contains a
// pre-defined human-readable message and NOT the raw err.Error() string.
// This protects against a regression that propagated internal SQL details
// to the client via the Location header.
func TestHandleThemeDelete_RedirectErrorIsSanitized(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	// Trigger ErrCannotDeleteDefault (a sentinel from the service).
	def, err := deps.Themes.GetDefault(context.Background())
	if err != nil {
		t.Fatalf("get default: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/themes/"+def.ID+"/delete", nil)
	req.SetPathValue("id", def.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleThemeDelete(deps.Themes).ServeHTTP(rr, req)

	loc := rr.Header().Get("Location")
	// The redirect must contain the human-readable, URL-encoded message.
	if !strings.Contains(loc, "error=Cannot+delete+the+default+theme") {
		t.Errorf("Location = %q, want error=Cannot+delete+the+default+theme", loc)
	}
	// And it must NOT contain the raw sentinel string from
	// errors.New("cannot delete the default theme") with quotes / colons,
	// nor SQL noise. We assert no leakage of "themes.go" or "internal/"
	// substrings (markers of a wrapped error chain).
	if strings.Contains(loc, "themes.go") || strings.Contains(loc, "internal/") {
		t.Errorf("Location = %q, leaks internal package paths", loc)
	}
}

// TestThemeRoutes_WrongMethodRejected verifies that requests using methods
// not registered on a route are rejected before any state mutation happens.
// The exact rejection code depends on the chain: GET on a POST-only route
// hits the mux as 405; non-GET state-changing methods (DELETE, PUT) hit
// CSRF first and get 403. Either is a hard rejection -- the failure
// scenario this test guards against is "the wrong method was silently
// honoured" (i.e., a 200 list page or a 302 success).
func TestThemeRoutes_WrongMethodRejected(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	target, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "method-check",
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
		t.Fatalf("create: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	tests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/admin/themes/" + target.ID + "/delete"},
		{http.MethodGet, "/admin/themes/" + target.ID + "/set-default"},
		{http.MethodDelete, "/admin/themes/" + target.ID},
		{http.MethodPut, "/admin/themes/" + target.ID},
	}
	for _, tt := range tests {
		t.Run(tt.method+"_"+tt.path, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, srv.URL+tt.path, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})
			req.Header.Set("Accept", "text/html")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", tt.method, tt.path, err)
			}
			defer resp.Body.Close()

			// 403 (CSRF rejection on a state-changing method without a
			// token), 404 (no route matched), or 405 (mux rejected the
			// method). Anything else means the wrong method was honoured.
			switch resp.StatusCode {
			case http.StatusForbidden, http.StatusMethodNotAllowed, http.StatusNotFound:
				// OK
			default:
				t.Errorf("status = %d, want 403/404/405 (wrong method should not be honoured)", resp.StatusCode)
			}
		})
	}

	// Theme row must still exist -- no method-confusion deletion.
	if _, err := deps.Themes.GetByID(context.Background(), target.ID); err != nil {
		t.Errorf("theme should still exist after method-confusion attempts; err = %v", err)
	}
}

// TestThemeRoutes_PathTraversalSafelyRejected verifies that path-traversal
// attempts in the URL are normalised by ServeMux before routing. Both raw
// `/..` and percent-encoded `%2e%2e` should fail to land on the
// /admin/users handler. The mux either redirects to the canonical path
// (which then 405s or 404s), or returns 405 directly.
func TestThemeRoutes_PathTraversalSafelyRejected(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	tests := []string{
		"/admin/themes/../users",
		"/admin/themes/%2e%2e/users",
	}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})
			req.Header.Set("Accept", "text/html")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close()
			// A successful 200 on /admin/users would be a routing bug.
			// The acceptable outcomes: 301/302/308 (redirect to canonical),
			// 404 (no match), or 405 (wrong method on a normalized path).
			if resp.StatusCode == http.StatusOK {
				t.Errorf("path %q rendered a 200 page; traversal was not blocked", path)
			}
		})
	}
}

// TestHandleThemeCreate_ValidatorRejectsScriptInjection verifies that even
// when the user submits an HTML break-out attempt in the name, the
// validator rejects it AND the rejected value, when re-rendered into the
// form, is HTML-escaped (templ does this automatically) so it cannot
// execute as a script. This is defence-in-depth: validation prevents
// storage; templ escaping prevents accidental execution if the value is
// echoed back.
func TestHandleThemeCreate_ValidatorRejectsScriptInjection(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	form := validThemeForm("ok")
	form.Set("name", `<img src=x onerror=alert(1)>`)

	req := httptest.NewRequest(http.MethodPost, "/admin/themes", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleThemeCreate(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (validator rejection re-renders inline)", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	// The literal break-out string MUST NOT appear unescaped in the body.
	if strings.Contains(body, `<img src=x onerror=alert(1)>`) {
		t.Error("body contains unescaped <img onerror=...> -- templ escaping failed")
	}
	// The escaped form should appear (in the value="..." attribute).
	if !strings.Contains(body, `&lt;img src=x onerror=alert(1)&gt;`) {
		t.Error("body should contain the HTML-escaped form of the rejected name")
	}
}

// TestHandleThemeUpdate_ConcurrentDeletesOneSucceeds verifies that two
// parallel deletes against the same non-default theme produce one success
// and one ErrThemeNotFound -- never a 500 panic, never two successes.
// This guards the boundary between the GetByID lookup and the DELETE,
// where a TOCTOU window exists.
func TestHandleThemeUpdate_ConcurrentDeletesOneSucceeds(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	created, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "race-target",
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
		t.Fatalf("create: %v", err)
	}

	const goroutines = 4
	type result struct {
		status int
		loc    string
	}
	results := make([]result, goroutines)

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/admin/themes/"+created.ID+"/delete", nil)
			req.SetPathValue("id", created.ID)
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()
			handleThemeDelete(deps.Themes).ServeHTTP(rr, req)
			results[i] = result{status: rr.Code, loc: rr.Header().Get("Location")}
		}(i)
	}
	wg.Wait()

	// Every concurrent caller should have received a 302 redirect (success
	// or error) -- never a 500.
	successCount := 0
	notFoundCount := 0
	for _, r := range results {
		if r.status != http.StatusFound {
			t.Errorf("status = %d, want %d (parallel handlers must not 500)", r.status, http.StatusFound)
			continue
		}
		switch {
		case strings.Contains(r.loc, "msg=deleted"):
			successCount++
		case strings.Contains(r.loc, "error=Theme+not+found"):
			notFoundCount++
		case strings.Contains(r.loc, "error=Could+not+delete+theme"):
			// The "delete theme: no row removed" path (race won by another
			// goroutine after GetByID succeeded but before DELETE matched
			// a row) surfaces as a generic error redirect. Acceptable.
			notFoundCount++
		default:
			t.Errorf("unexpected redirect location %q", r.loc)
		}
	}
	if successCount != 1 {
		t.Errorf("successful deletes = %d, want 1 (exactly one goroutine should have succeeded)", successCount)
	}
	if notFoundCount != goroutines-1 {
		t.Errorf("not-found deletes = %d, want %d", notFoundCount, goroutines-1)
	}

	// Final state: the row is gone.
	if _, err := deps.Themes.GetByID(context.Background(), created.ID); err == nil {
		t.Error("row should be deleted after concurrent attempts; GetByID succeeded unexpectedly")
	}
}

// TestThemeMgmt_SetDefaultExactlyOneRemains verifies that two concurrent
// set-default calls against different themes leave the system with
// exactly one default. Final state validated via the underlying queries.
func TestThemeMgmt_SetDefaultExactlyOneRemains(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	a, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "alpha",
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
		t.Fatalf("create alpha: %v", err)
	}
	b, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "bravo",
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
		t.Fatalf("create bravo: %v", err)
	}

	var wg sync.WaitGroup
	for _, id := range []string{a.ID, b.ID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/admin/themes/"+id+"/set-default", nil)
			req.SetPathValue("id", id)
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()
			handleThemeSetDefault(deps.Themes).ServeHTTP(rr, req)
			if rr.Code != http.StatusFound {
				t.Errorf("set-default status = %d, want %d", rr.Code, http.StatusFound)
			}
		}(id)
	}
	wg.Wait()

	// Use the lower-level query to verify exactly one row has is_default=1.
	list, err := deps.Themes.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defaults := 0
	for _, th := range list {
		if th.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("default count = %d, want 1 (the partial unique index plus the SetDefault transaction must enforce exactly-one)", defaults)
	}
}

// TestThemeMgmt_LogsDoNotContainCSRFToken verifies that the slog lines
// emitted on theme actions do not include the CSRFToken or session token.
// A regression that added "csrf" or "session" attributes to the log line
// could leak credentials.
func TestThemeMgmt_LogsDoNotContainCSRFToken(t *testing.T) {
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})
	session := auth.SessionFromContext(ctx)
	if session == nil {
		t.Fatal("session missing from test context")
	}

	// Capture slog output. Cannot run in parallel because slog.SetDefault
	// is a process-global mutation.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	form := validThemeForm("logged-create")
	req := httptest.NewRequest(http.MethodPost, "/admin/themes", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleThemeCreate(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}

	logged := buf.String()
	if strings.Contains(logged, session.CSRFToken) {
		t.Errorf("log output leaks CSRF token; output = %q", logged)
	}
	// Sanity: the create line is present in the log so the test isn't
	// silently exercising the wrong path.
	if !strings.Contains(logged, "theme created") {
		t.Errorf("log output missing 'theme created' line; output = %q", logged)
	}
}
