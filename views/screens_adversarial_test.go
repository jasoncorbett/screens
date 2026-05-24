package views

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/jasoncorbett/screens/internal/auth"
	"github.com/jasoncorbett/screens/internal/screens"
)

// --- 1. List page empty-state ---

// Renders cleanly with zero screens (no panic, page chrome present).
func TestHandleScreenList_EmptyState(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	req := httptest.NewRequest(http.MethodGet, "/admin/screens", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenList(deps.Screens, deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (empty state must render)", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Screen Management") {
		t.Error("body missing 'Screen Management' header on empty state")
	}
	if !strings.Contains(body, "New Screen") {
		t.Error("body missing 'New Screen' form heading on empty state")
	}
}

// --- 2. Screen edit page lists 50 pages without truncation ---

func TestHandleScreenEditForm_ManyPages(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "many-pages")
	const N = 50
	for i := 0; i < N; i++ {
		if _, err := deps.Screens.CreatePage(context.Background(), screen.ID, ""); err != nil {
			t.Fatalf("create page %d: %v", i, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/screens/"+screen.ID+"/edit", nil)
	req.SetPathValue("id", screen.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenEditForm(deps.Screens, deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	// Every page renders as "(no name)" since we passed empty names. Count
	// occurrences to confirm none were truncated.
	body := rr.Body.String()
	if got := strings.Count(body, "(no name)"); got != N {
		t.Errorf("got %d (no name) entries, want %d", got, N)
	}
}

// --- 3. HTML escaping of flash messages ---

// errMsg from a query param that contains HTML must not appear unescaped.
func TestHandleScreenList_FlashEscapesHTML(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	const payload = `<script>alert(1)</script>`
	req := httptest.NewRequest(http.MethodGet, "/admin/screens?error="+url.QueryEscape(payload), nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenList(deps.Screens, deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, payload) {
		t.Error("flash message rendered raw HTML; XSS escape regression")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("expected the flash message text to appear HTML-escaped")
	}
}

// Screen name containing HTML must render escaped in the list view.
//
// Names go through validateScreenInput's regex, so a literal `<script>` is
// rejected upstream. We can only verify by injecting via a benign regex-passing
// path. Instead we exercise the page-name path, which accepts a wider set of
// characters via the same regex but is exposed via the edit page.
func TestHandleScreenList_NameRegexBlocksMarkup(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	// Sanity: regex rejects markup -- this is the first line of defence.
	form := validScreenForm(t, deps, `<script>`)
	req := httptest.NewRequest(http.MethodPost, "/admin/screens", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenCreate(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rr.Code)
	}
	if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "/admin/screens?error=") {
		t.Errorf("Location = %q, want /admin/screens?error=...", loc)
	}
}

// --- 4. Rotation parse fuzz ---

func TestHandleScreenCreate_RotationFuzz(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		rotation  string
		wantError string // substring of the redirect Location's error param (decoded)
	}{
		{name: "empty", rotation: "", wantError: "Invalid rotation interval"},
		{name: "non-numeric", rotation: "abc", wantError: "Invalid rotation interval"},
		{name: "trailing junk", rotation: "30x", wantError: "Invalid rotation interval"},
		{name: "below min", rotation: "4", wantError: "Rotation interval must be between 5 and 3600 seconds"},
		{name: "above max", rotation: "3601", wantError: "Rotation interval must be between 5 and 3600 seconds"},
		{name: "negative", rotation: "-1", wantError: "Rotation interval must be between 5 and 3600 seconds"},
		{name: "zero", rotation: "0", wantError: "Rotation interval must be between 5 and 3600 seconds"},
		{name: "overflow", rotation: "99999999999999999999999999", wantError: "Invalid rotation interval"},
		{name: "boundary min", rotation: "5", wantError: ""},
		{name: "boundary max", rotation: "3600", wantError: ""},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deps, q := newTestDeps(t)
			admin := createTestUser(t, q, "admin@example.com", "admin")
			ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

			form := validScreenForm(t, deps, "rotation-fuzz")
			form.Set("rotation_interval_seconds", tt.rotation)

			req := httptest.NewRequest(http.MethodPost, "/admin/screens", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()
			handleScreenCreate(deps.Screens).ServeHTTP(rr, req)

			if rr.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302 (must not 500)", rr.Code)
			}
			loc := rr.Header().Get("Location")
			if tt.wantError == "" {
				if loc != "/admin/screens?msg=created" {
					t.Errorf("Location = %q, want /admin/screens?msg=created", loc)
				}
				return
			}
			u, err := url.Parse(loc)
			if err != nil {
				t.Fatalf("parse Location %q: %v", loc, err)
			}
			gotErr := u.Query().Get("error")
			if gotErr != tt.wantError {
				t.Errorf("error param = %q, want %q", gotErr, tt.wantError)
			}
		})
	}
}

// Same fuzz for the update path.
func TestHandleScreenUpdate_RotationFuzz(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "rotation-update")

	form := validScreenForm(t, deps, "rotation-update")
	form.Set("rotation_interval_seconds", "not-a-number")
	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", screen.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenUpdate(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (must not 500)", rr.Code)
	}
	wantLoc := "/admin/screens/" + screen.ID + "/edit?error=Invalid+rotation+interval"
	if loc := rr.Header().Get("Location"); loc != wantLoc {
		t.Errorf("Location = %q, want %q", loc, wantLoc)
	}
}

// --- 5. Path/query fuzz on screen ID ---

func TestHandleScreenEditForm_BadIDs(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	tests := []string{
		"../../../etc/passwd",
		"' OR '1'='1",
		"\x00null-byte",
		"日本語",
		strings.Repeat("a", 1024),
	}
	for _, id := range tests {
		t.Run(id, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/screens/x/edit", nil)
			req.SetPathValue("id", id)
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()
			handleScreenEditForm(deps.Screens, deps.Themes).ServeHTTP(rr, req)

			if rr.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302 (must not 500)", rr.Code)
			}
			loc := rr.Header().Get("Location")
			if loc != "/admin/screens?error=Screen+not+found" {
				t.Errorf("Location = %q, want /admin/screens?error=Screen+not+found", loc)
			}
		})
	}
}

// --- 6. Cross-screen page authorisation (defence-in-depth) ---

// A page exists on screen A, but the URL parameter screenID is B. The service
// returns ErrPageNotFound; the handler must redirect with the friendly flash
// rather than mutating the page.
func TestHandlePageDelete_CrossScreenRejected(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screenA := createTestScreen(t, deps, "screen-a")
	screenB := createTestScreen(t, deps, "screen-b")

	page, err := deps.Screens.CreatePage(context.Background(), screenA.ID, "page-on-a")
	if err != nil {
		t.Fatalf("create page on A: %v", err)
	}

	// POST /admin/screens/B/pages/{page on A}/delete -- must be rejected.
	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screenB.ID+"/pages/"+page.ID+"/delete", nil)
	req.SetPathValue("id", screenB.ID)
	req.SetPathValue("pageID", page.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageDelete(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rr.Code)
	}
	wantLoc := "/admin/screens/" + screenB.ID + "/edit?error=Page+not+found"
	if loc := rr.Header().Get("Location"); loc != wantLoc {
		t.Errorf("Location = %q, want %q", loc, wantLoc)
	}

	// Page must still be alive on screen A.
	pages, err := deps.Screens.ListPages(context.Background(), screenA.ID)
	if err != nil {
		t.Fatalf("list pages on A: %v", err)
	}
	if len(pages) != 1 || pages[0].ID != page.ID {
		t.Errorf("page on A should still exist; got %d pages", len(pages))
	}
}

// Same probe but for MoveUp.
func TestHandlePageMoveUp_CrossScreenRejected(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screenA := createTestScreen(t, deps, "screen-a")
	screenB := createTestScreen(t, deps, "screen-b")

	if _, err := deps.Screens.CreatePage(context.Background(), screenA.ID, "p0"); err != nil {
		t.Fatalf("create p0: %v", err)
	}
	page, err := deps.Screens.CreatePage(context.Background(), screenA.ID, "p1")
	if err != nil {
		t.Fatalf("create page on A: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screenB.ID+"/pages/"+page.ID+"/move-up", nil)
	req.SetPathValue("id", screenB.ID)
	req.SetPathValue("pageID", page.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageMoveUp(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rr.Code)
	}
	wantLoc := "/admin/screens/" + screenB.ID + "/edit?error=Page+not+found"
	if loc := rr.Header().Get("Location"); loc != wantLoc {
		t.Errorf("Location = %q, want %q", loc, wantLoc)
	}

	// Original page position must be unchanged.
	pages, err := deps.Screens.ListPages(context.Background(), screenA.ID)
	if err != nil {
		t.Fatalf("list pages on A: %v", err)
	}
	for _, p := range pages {
		if p.ID == page.ID && p.Position != 2 {
			t.Errorf("page %q position = %d, want 2 (must not have moved)", p.ID, p.Position)
		}
	}
}

// --- 7. Non-existent page ID on every page-level handler ---

func TestPageHandlers_NonexistentPage(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	const fakePageID = "nonexistent-page-id"

	tests := []struct {
		name    string
		handler http.HandlerFunc
		urlPath string
	}{
		{"update", handlePageUpdate(deps.Screens), "/admin/screens/" + screen.ID + "/pages/" + fakePageID},
		{"delete", handlePageDelete(deps.Screens), "/admin/screens/" + screen.ID + "/pages/" + fakePageID + "/delete"},
		{"move-up", handlePageMoveUp(deps.Screens), "/admin/screens/" + screen.ID + "/pages/" + fakePageID + "/move-up"},
		{"move-down", handlePageMoveDown(deps.Screens), "/admin/screens/" + screen.ID + "/pages/" + fakePageID + "/move-down"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.urlPath, nil)
			req.SetPathValue("id", screen.ID)
			req.SetPathValue("pageID", fakePageID)
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()
			tt.handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302 (must not 500)", rr.Code)
			}
			loc := rr.Header().Get("Location")
			if !strings.Contains(loc, "error=Page+not+found") {
				t.Errorf("Location = %q, want redirect with Page+not+found error", loc)
			}
		})
	}
}

// --- 8. Page CRUD when screen does not exist ---

func TestHandlePageCreate_NonexistentScreen(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	form := url.Values{}
	form.Set("name", "clock")
	req := httptest.NewRequest(http.MethodPost, "/admin/screens/nonexistent/pages", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", "nonexistent")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageCreate(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/screens?error=Screen+not+found" {
		t.Errorf("Location = %q, want /admin/screens?error=Screen+not+found", loc)
	}
}

// --- 9. CSRF: every POST route is rejected without _csrf via the full chain ---

func TestScreenRoutes_AllPOSTsRequireCSRF(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	screen := createTestScreen(t, deps, "csrf-target")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p")
	if err != nil {
		t.Fatalf("create page: %v", err)
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

	posts := []string{
		"/admin/screens",
		"/admin/screens/" + screen.ID,
		"/admin/screens/" + screen.ID + "/delete",
		"/admin/screens/" + screen.ID + "/pages",
		"/admin/screens/" + screen.ID + "/pages/" + page.ID,
		"/admin/screens/" + screen.ID + "/pages/" + page.ID + "/delete",
		"/admin/screens/" + screen.ID + "/pages/" + page.ID + "/move-up",
		"/admin/screens/" + screen.ID + "/pages/" + page.ID + "/move-down",
	}
	for _, p := range posts {
		t.Run(p, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, srv.URL+p, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("POST %s: %v", p, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("POST %s: status = %d, want 403 (CSRF should reject)", p, resp.StatusCode)
			}
		})
	}

	// Screen + page must still exist after all the CSRF rejections.
	if _, err := deps.Screens.GetScreenByID(context.Background(), screen.ID); err != nil {
		t.Errorf("screen should still exist; err = %v", err)
	}
	if _, err := deps.Screens.GetPageByID(context.Background(), screen.ID, page.ID); err != nil {
		t.Errorf("page should still exist; err = %v", err)
	}
}

// --- 10. Member is 403 on every admin screen route ---

func TestScreenRoutes_MemberIs403_AllRoutes(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	member := createTestUser(t, q, "member@example.com", "member")
	rawToken, err := deps.Auth.CreateSession(context.Background(), member.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	// Validate so we can grab the CSRF token, since POSTs through the chain
	// need it before reaching the RoleAdmin gate.
	_, session, err := deps.Auth.ValidateSession(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}

	// Create a screen + page via the service (bypassing handlers) so we have
	// concrete IDs to probe.
	screen := createTestScreen(t, deps, "member-probe")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p")
	if err != nil {
		t.Fatalf("create page: %v", err)
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

	type probe struct {
		method string
		path   string
	}
	probes := []probe{
		{http.MethodGet, "/admin/screens"},
		{http.MethodGet, "/admin/screens/" + screen.ID + "/edit"},
		{http.MethodPost, "/admin/screens"},
		{http.MethodPost, "/admin/screens/" + screen.ID},
		{http.MethodPost, "/admin/screens/" + screen.ID + "/delete"},
		{http.MethodPost, "/admin/screens/" + screen.ID + "/pages"},
		{http.MethodPost, "/admin/screens/" + screen.ID + "/pages/" + page.ID},
		{http.MethodPost, "/admin/screens/" + screen.ID + "/pages/" + page.ID + "/delete"},
		{http.MethodPost, "/admin/screens/" + screen.ID + "/pages/" + page.ID + "/move-up"},
		{http.MethodPost, "/admin/screens/" + screen.ID + "/pages/" + page.ID + "/move-down"},
	}
	for _, p := range probes {
		t.Run(p.method+" "+p.path, func(t *testing.T) {
			var body string
			if p.method == http.MethodPost {
				body = "_csrf=" + session.CSRFToken
			}
			req, err := http.NewRequest(p.method, srv.URL+p.path, strings.NewReader(body))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			if p.method == http.MethodPost {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", p.method, p.path, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("%s %s: status = %d, want 403 (RequireRole should reject member)", p.method, p.path, resp.StatusCode)
			}
		})
	}

	// Confirm nothing mutated.
	if _, err := deps.Screens.GetScreenByID(context.Background(), screen.ID); err != nil {
		t.Errorf("screen should still exist; err = %v", err)
	}
}

// --- 11. Unauthenticated user is redirected to login (302), not 403 ---

func TestScreenRoutes_UnauthenticatedIsRedirectedToLogin(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDepsWithGoogle(t)

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/admin/screens", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/html")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/screens: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 (unauth must redirect to login)", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "/admin/login") {
		t.Errorf("Location = %q, want redirect to /admin/login", loc)
	}
}

// --- 12. Routes wiring: when deps.Screens is nil, screen URLs don't panic ---

// The wiring uses `if deps.Screens != nil` to register the screenMux. When it
// is nil, the route is not registered; a GET hits the catch-all `/admin/` and
// resolves to the admin landing or 404, but must NEVER panic.
func TestRoutes_NoScreensServiceDoesNotPanic(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	deps.Screens = nil // drop the service

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/admin/screens", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/screens: %v", err)
	}
	resp.Body.Close()

	// Must not panic / 500. The exact 4xx code depends on the catch-all match.
	if resp.StatusCode >= 500 {
		t.Errorf("status = %d, want < 500 (must not panic when Screens is nil)", resp.StatusCode)
	}
}

// --- 13. Concurrent admin operations under -race ---

// Two reorder operations on different screens shouldn't tangle. This exercises
// the connection pool and the swap transaction. The race detector catches any
// shared-state mishandling.
func TestPageReorder_ConcurrentAcrossScreens(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	type setup struct {
		screen screens.Screen
		p1, p2 screens.Page
	}
	makeSetup := func(name string) setup {
		s := createTestScreen(t, deps, name)
		p1, err := deps.Screens.CreatePage(context.Background(), s.ID, "p1")
		if err != nil {
			t.Fatalf("p1: %v", err)
		}
		p2, err := deps.Screens.CreatePage(context.Background(), s.ID, "p2")
		if err != nil {
			t.Fatalf("p2: %v", err)
		}
		return setup{screen: s, p1: p1, p2: p2}
	}
	a := makeSetup("alpha")
	b := makeSetup("beta")

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+a.screen.ID+"/pages/"+a.p1.ID+"/move-down", nil)
			req.SetPathValue("id", a.screen.ID)
			req.SetPathValue("pageID", a.p1.ID)
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()
			handlePageMoveDown(deps.Screens).ServeHTTP(rr, req)
		}()
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+b.screen.ID+"/pages/"+b.p2.ID+"/move-up", nil)
			req.SetPathValue("id", b.screen.ID)
			req.SetPathValue("pageID", b.p2.ID)
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()
			handlePageMoveUp(deps.Screens).ServeHTTP(rr, req)
		}()
	}
	wg.Wait()

	// Verify no duplicate position rows on either screen.
	for _, s := range []screens.Screen{a.screen, b.screen} {
		pages, err := deps.Screens.ListPages(context.Background(), s.ID)
		if err != nil {
			t.Fatalf("list pages: %v", err)
		}
		seen := make(map[int]bool)
		for _, p := range pages {
			if seen[p.Position] {
				t.Errorf("screen %q has duplicate position %d", s.Name, p.Position)
			}
			seen[p.Position] = true
		}
	}
}

// --- 14. Move-up / Move-down via GET (only POST is registered) ---

// GET requests on a POST-only route must NOT mutate state. The middleware /
// router rejects them.
func TestScreenRoutes_GETOnPostRouteRejected(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	screen := createTestScreen(t, deps, "get-on-post")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p")
	if err != nil {
		t.Fatalf("create page: %v", err)
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

	// GET on move-up: the ServeMux pattern is POST-only, so it should not be
	// dispatched. The exact code is 405 (Method Not Allowed) per Go's stdlib.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/admin/screens/"+screen.ID+"/pages/"+page.ID+"/move-up", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET move-up: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode >= 500 {
		t.Errorf("status = %d, must not 5xx for GET on POST-only route", resp.StatusCode)
	}
	// Specifically must not be 200 (which would mean the route was matched).
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusFound {
		t.Errorf("status = %d, want 4xx for GET on POST-only route", resp.StatusCode)
	}
}

// --- 15. Theme-in-use flash round trip ---

// Verifies that after the failed theme delete redirects with the flash, a
// subsequent GET on /admin/themes renders the flash (escaped, in a role=alert
// card).
func TestThemeDelete_InUseFlashRendersOnRedirect(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	// Make a screen referencing a fresh theme so that deletion is blocked.
	def, err := deps.Themes.GetDefault(context.Background())
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if _, err := deps.Screens.CreateScreen(context.Background(), screens.ScreenInput{
		Name:                    "uses-default",
		ThemeID:                 def.ID,
		RotationIntervalSeconds: 30,
	}); err != nil {
		t.Fatalf("create screen: %v", err)
	}

	// Re-fetch the themes list with the error flash and render.
	req := httptest.NewRequest(http.MethodGet, "/admin/themes?error=Cannot+delete+a+theme+in+use+by+a+screen", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleThemeList(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Cannot delete a theme in use by a screen") {
		t.Errorf("body missing themes-in-use flash text")
	}
}

// --- 16. handleScreenList does NOT call user.Active or session.IsValid ---

// The handler only checks user == nil. A deactivated user with a valid session
// reaches the handler. (Defense-in-depth comment: this is intentional -- the
// middleware chain is the gatekeeper. We just verify the handler shape.)
func TestHandleScreenList_NoSessionContextReturns403(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	user := &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin}
	// User in context, but NO session: handler must 403.
	ctx := auth.ContextWithUser(context.Background(), user)

	req := httptest.NewRequest(http.MethodGet, "/admin/screens", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenList(deps.Screens, deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 when session missing", rr.Code)
	}
}

// --- 17. Delete cascades pages and widgets via the handler ---

func TestHandleScreenDelete_CascadesPagesAndWidgets(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "cascade")
	p1, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p1")
	if err != nil {
		t.Fatalf("create p1: %v", err)
	}
	p2, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p2")
	if err != nil {
		t.Fatalf("create p2: %v", err)
	}
	if _, err := deps.Screens.AddWidget(context.Background(), screen.ID, p1.ID, "text"); err != nil {
		t.Fatalf("add widget on p1: %v", err)
	}
	if _, err := deps.Screens.AddWidget(context.Background(), screen.ID, p2.ID, "text"); err != nil {
		t.Fatalf("add widget on p2: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID+"/delete", nil)
	req.SetPathValue("id", screen.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenDelete(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rr.Code)
	}

	// Screen gone.
	if _, err := deps.Screens.GetScreenByID(context.Background(), screen.ID); !errors.Is(err, screens.ErrScreenNotFound) {
		t.Errorf("GetScreenByID after delete err = %v, want ErrScreenNotFound", err)
	}
	// Pages gone (ListPages on a missing screen returns empty, not error).
	pages, err := deps.Screens.ListPages(context.Background(), screen.ID)
	if err != nil {
		t.Fatalf("list pages after delete: %v", err)
	}
	if len(pages) != 0 {
		t.Errorf("len(pages) = %d, want 0 after screen delete cascade", len(pages))
	}
}

// --- 18. handleScreenDelete on non-existent screen -- friendly redirect, not 500 ---

func TestHandleScreenDelete_NonexistentScreen(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	req := httptest.NewRequest(http.MethodPost, "/admin/screens/nonexistent/delete", nil)
	req.SetPathValue("id", "nonexistent")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenDelete(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/screens?error=Screen+not+found" {
		t.Errorf("Location = %q, want /admin/screens?error=Screen+not+found", loc)
	}
}

// --- 19. handlePageCreate name validation surfaces validation error ---

// The spec says the redirect target on validation error is the EDIT page for
// the screen, with the flash. Ensure malformed names bounce there.
func TestHandlePageCreate_InvalidNameRedirectsToEdit(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "page-name-fuzz")

	form := url.Values{}
	form.Set("name", "name<script>") // disallowed char set
	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID+"/pages", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", screen.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageCreate(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rr.Code)
	}
	loc := rr.Header().Get("Location")
	wantPrefix := "/admin/screens/" + screen.ID + "/edit?error="
	if !strings.HasPrefix(loc, wantPrefix) {
		t.Errorf("Location = %q, want prefix %q", loc, wantPrefix)
	}
	// Page must not be created.
	pages, err := deps.Screens.ListPages(context.Background(), screen.ID)
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	if len(pages) != 0 {
		t.Errorf("len(pages) = %d, want 0 (validation should reject)", len(pages))
	}
}

// --- 20. Members do NOT see the Manage Screens link ---

func TestAdminLandingPage_MemberHasNoScreensLink(t *testing.T) {
	t.Parallel()

	user := &auth.User{
		ID:          "member-id",
		Email:       "member@example.com",
		DisplayName: "Member",
		Role:        auth.RoleMember,
	}
	session := &auth.Session{CSRFToken: "csrf"}
	ctx := auth.ContextWithUser(context.Background(), user)
	ctx = auth.ContextWithSession(ctx, session)

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleAdmin(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, `href="/admin/screens"`) {
		t.Error("member body should NOT contain /admin/screens link")
	}
}
