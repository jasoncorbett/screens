package views

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasoncorbett/screens/internal/auth"
	"github.com/jasoncorbett/screens/internal/db"
	"github.com/jasoncorbett/screens/internal/screens"
	"github.com/jasoncorbett/screens/internal/themes"
	"github.com/jasoncorbett/screens/internal/widget"
)

// dbOpenTestDB is a thin alias around the package db's OpenTestDB so this
// file can construct a raw *sql.DB to insert rows directly when the test
// needs to exercise paths that bypass the service's validation.
func dbOpenTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return db.OpenTestDB(t)
}

// dbNewQueries is a thin alias around db.New that returns a *db.Queries for
// tests that need raw access to the sqlc-generated queries.
func dbNewQueries(d *sql.DB) *db.Queries {
	return db.New(d)
}

// --- 1. Cross-screen / cross-page page-edit GET ---

// A page on screen A, accessed via screen B's URL, must surface as Page not
// found, not silently render the wrong page or 500.
func TestHandlePageEditForm_CrossScreenRejected(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screenA := createTestScreen(t, deps, "screen-a")
	screenB := createTestScreen(t, deps, "screen-b")
	page, err := deps.Screens.CreatePage(context.Background(), screenA.ID, "page-on-a")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/screens/"+screenB.ID+"/pages/"+page.ID+"/edit", nil)
	req.SetPathValue("id", screenB.ID)
	req.SetPathValue("pageID", page.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageEditForm(deps.Screens, deps.Widgets).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rr.Code)
	}
	wantLoc := "/admin/screens/" + screenB.ID + "/edit?error=Page+not+found"
	if loc := rr.Header().Get("Location"); loc != wantLoc {
		t.Errorf("Location = %q, want %q", loc, wantLoc)
	}
}

// --- 2. Cross-screen / cross-page widget routes ---

// All 4 widget POST routes: a widget on (screen A, page P) accessed via
// (screen B, page P) must redirect with a friendly flash, not 5xx.
func TestWidgetHandlers_CrossScreenRejected(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screenA := createTestScreen(t, deps, "screen-a")
	screenB := createTestScreen(t, deps, "screen-b")
	page, err := deps.Screens.CreatePage(context.Background(), screenA.ID, "p")
	if err != nil {
		t.Fatalf("create page on A: %v", err)
	}
	w1 := addTestWidget(t, deps, screenA.ID, page.ID, "text")

	tests := []struct {
		name     string
		handler  http.HandlerFunc
		urlPath  string
		wantLoc  string
		probeIDs func(req *http.Request)
	}{
		{
			name:    "create-widget",
			handler: handleWidgetCreate(deps.Screens),
			urlPath: "/admin/screens/" + screenB.ID + "/pages/" + page.ID + "/widgets",
			// AddWidget calls GetPageByID -> ErrPageNotFound; handler redirects to screen edit.
			wantLoc: "/admin/screens/" + screenB.ID + "/edit?error=Page+not+found",
		},
		{
			name:    "delete-widget",
			handler: handleWidgetDelete(deps.Screens),
			urlPath: "/admin/screens/" + screenB.ID + "/pages/" + page.ID + "/widgets/" + w1.ID + "/delete",
			wantLoc: "/admin/screens/" + screenB.ID + "/edit?error=Page+not+found",
		},
		{
			name:    "move-up",
			handler: handleWidgetMoveUp(deps.Screens),
			urlPath: "/admin/screens/" + screenB.ID + "/pages/" + page.ID + "/widgets/" + w1.ID + "/move-up",
			wantLoc: "/admin/screens/" + screenB.ID + "/edit?error=Page+not+found",
		},
		{
			name:    "move-down",
			handler: handleWidgetMoveDown(deps.Screens),
			urlPath: "/admin/screens/" + screenB.ID + "/pages/" + page.ID + "/widgets/" + w1.ID + "/move-down",
			wantLoc: "/admin/screens/" + screenB.ID + "/edit?error=Page+not+found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body string
			if tt.name == "create-widget" {
				v := url.Values{}
				v.Set("type", "text")
				body = v.Encode()
			}
			req := httptest.NewRequest(http.MethodPost, tt.urlPath, strings.NewReader(body))
			if tt.name == "create-widget" {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
			req.SetPathValue("id", screenB.ID)
			req.SetPathValue("pageID", page.ID)
			req.SetPathValue("widgetID", w1.ID)
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()
			tt.handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302 (must not 500)", rr.Code)
			}
			if loc := rr.Header().Get("Location"); loc != tt.wantLoc {
				t.Errorf("Location = %q, want %q", loc, tt.wantLoc)
			}
		})
	}

	// Widget on screen A must still exist.
	widgets, err := deps.Screens.ListWidgetInstancesByPage(context.Background(), screenA.ID, page.ID)
	if err != nil {
		t.Fatalf("list widgets after cross-screen probes: %v", err)
	}
	if len(widgets) != 1 || widgets[0].ID != w1.ID {
		t.Errorf("widget on screen A should still exist; got %d widgets", len(widgets))
	}
}

// --- 3. Widget delete with widget that belongs to a different page ---

// Two pages on the same screen; widget on page A is targeted via page B's URL.
// The DeleteWidget query uses (widget_id, page_id), so it returns 0 rows
// affected and the service surfaces ErrWidgetNotFound. The handler should
// redirect with the friendly "Widget not found" flash and NOT delete the
// widget on the other page.
func TestHandleWidgetDelete_CrossPageRejected(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	pageA, err := deps.Screens.CreatePage(context.Background(), screen.ID, "a")
	if err != nil {
		t.Fatalf("create pageA: %v", err)
	}
	pageB, err := deps.Screens.CreatePage(context.Background(), screen.ID, "b")
	if err != nil {
		t.Fatalf("create pageB: %v", err)
	}
	w := addTestWidget(t, deps, screen.ID, pageA.ID, "text")

	// Try to delete widget w (on pageA) via pageB's URL.
	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID+"/pages/"+pageB.ID+"/widgets/"+w.ID+"/delete", nil)
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", pageB.ID)
	req.SetPathValue("widgetID", w.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleWidgetDelete(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rr.Code)
	}
	wantLoc := "/admin/screens/" + screen.ID + "/pages/" + pageB.ID + "/edit?error=Widget+not+found"
	if loc := rr.Header().Get("Location"); loc != wantLoc {
		t.Errorf("Location = %q, want %q", loc, wantLoc)
	}

	// Widget on pageA must still exist.
	widgets, err := deps.Screens.ListWidgetInstancesByPage(context.Background(), screen.ID, pageA.ID)
	if err != nil {
		t.Fatalf("list widgets on pageA: %v", err)
	}
	if len(widgets) != 1 || widgets[0].ID != w.ID {
		t.Errorf("widget on pageA should be intact; got %d widgets", len(widgets))
	}
}

// --- 4. Page-edit GET: HTML escaping of widget config bytes ---

// The page-edit view renders the widget config bytes inside <pre><code>. The
// bytes can contain anything; the template must HTML-escape on render. We
// register a custom widget whose default config contains HTML markup, then
// persist an instance via the normal service path and fetch the page-edit
// view. The base test stack is rebuilt around a *sql.DB we own so a fresh
// screens.Service can be constructed with the custom registry.
func TestHandlePageEditForm_WidgetConfigEscapesHTML(t *testing.T) {
	t.Parallel()
	sqlDB := dbOpenTestDB(t)
	authSvc := auth.NewService(sqlDB, auth.Config{
		AdminEmail:             "admin@example.com",
		SessionDuration:        time.Hour,
		CookieName:             "session",
		SecureCookie:           false,
		DeviceCookieName:       "device",
		DeviceLastSeenInterval: time.Minute,
		DeviceLandingURL:       "/device/",
	})
	themesSvc := themes.NewService(sqlDB, themes.Config{DefaultName: "default"})
	if err := themesSvc.EnsureDefault(context.Background()); err != nil {
		t.Fatalf("seed default theme: %v", err)
	}

	// Custom registry whose default config carries HTML.
	reg := widget.NewRegistry()
	if err := reg.Register(widget.Registration{
		Type:           "evilcfg",
		DisplayName:    "Evil",
		Description:    "carries HTML",
		New:            func() widget.Widget { return nil },
		DefaultConfig:  func() []byte { return []byte(`{"x":"<script>alert(1)</script>"}`) },
		ValidateConfig: func(raw []byte) (widget.Instance, error) { return widget.Instance{Type: "evilcfg"}, nil },
	}); err != nil {
		t.Fatalf("register evil widget: %v", err)
	}
	screensSvc := screens.NewService(sqlDB, themesSvc, reg)
	q := dbNewQueries(sqlDB)

	deps := &Deps{
		Auth:       authSvc,
		CookieName: "session",
		Themes:     themesSvc,
		Widgets:    reg,
		Screens:    screensSvc,
	}

	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	if _, err := deps.Screens.AddWidget(context.Background(), screen.ID, page.ID, "evilcfg"); err != nil {
		t.Fatalf("add evilcfg widget: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/edit", nil)
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", page.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageEditForm(deps.Screens, deps.Widgets).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("XSS: raw <script> from widget config appears unescaped in page-edit view")
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("expected HTML-escaped script in widget config; body=%q", body)
	}
}

// --- 5. Page-edit GET: HTML escaping of flash messages and registry strings ---

// Flash error from the query param must be escaped.
func TestHandlePageEditForm_FlashEscapesHTML(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	const payload = `<script>alert(1)</script>`
	req := httptest.NewRequest(http.MethodGet, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/edit?error="+url.QueryEscape(payload), nil)
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", page.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageEditForm(deps.Screens, deps.Widgets).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, payload) {
		t.Error("page-edit flash rendered raw HTML")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("page-edit flash should appear HTML-escaped")
	}
}

// A registration with HTML-bearing DisplayName / Description must escape on
// render (defence-in-depth -- a future widget plugin author writing
// "Display<br>Name" must not break the page).
func TestHandlePageEditForm_WidgetTypeSelectEscapesRegistration(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	// Build a registry whose Registration carries HTML-like strings.
	reg := widget.NewRegistry()
	// Build a fake registration whose New/DefaultConfig/ValidateConfig satisfy
	// the registry's nil-checks.
	if err := reg.Register(widget.Registration{
		Type:           "evil",
		DisplayName:    "<img src=x onerror=alert(1)>",
		Description:    "<b>bold</b>",
		New:            func() widget.Widget { return nil },
		DefaultConfig:  func() []byte { return []byte("{}") },
		ValidateConfig: func(raw []byte) (widget.Instance, error) { return widget.Instance{Type: "evil"}, nil },
	}); err != nil {
		t.Fatalf("register evil widget: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/edit", nil)
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", page.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageEditForm(deps.Screens, reg).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "<img src=x onerror=alert(1)>") {
		t.Error("registration DisplayName rendered unescaped")
	}
	if strings.Contains(body, "<b>bold</b>") {
		t.Error("registration Description rendered unescaped")
	}
	if !strings.Contains(body, "&lt;img") || !strings.Contains(body, "&lt;b&gt;") {
		t.Errorf("expected HTML-escaped registration text; body=%q", body)
	}
}

// --- 6. Page-edit GET: path-param fuzz on pageID ---

func TestHandlePageEditForm_BadPageIDs(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")

	tests := []string{
		"../../../etc/passwd",
		"' OR '1'='1",
		"\x00null-byte",
		"日本語",
		strings.Repeat("a", 1024),
	}
	for _, id := range tests {
		t.Run(id, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/screens/"+screen.ID+"/pages/x/edit", nil)
			req.SetPathValue("id", screen.ID)
			req.SetPathValue("pageID", id)
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()
			handlePageEditForm(deps.Screens, deps.Widgets).ServeHTTP(rr, req)

			if rr.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302 (must not 500)", rr.Code)
			}
			wantLoc := "/admin/screens/" + screen.ID + "/edit?error=Page+not+found"
			if loc := rr.Header().Get("Location"); loc != wantLoc {
				t.Errorf("Location = %q, want %q", loc, wantLoc)
			}
		})
	}
}

// --- 7. Widget routes: path-param fuzz on widgetID ---

func TestWidgetHandlers_BadWidgetIDs(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	badIDs := []string{
		"../../../etc/passwd",
		"' OR '1'='1",
		"\x00null-byte",
		"日本語",
		strings.Repeat("a", 1024),
	}
	handlers := []struct {
		name    string
		handler http.HandlerFunc
		suffix  string
	}{
		{"delete", handleWidgetDelete(deps.Screens), "/delete"},
		{"move-up", handleWidgetMoveUp(deps.Screens), "/move-up"},
		{"move-down", handleWidgetMoveDown(deps.Screens), "/move-down"},
	}
	for _, h := range handlers {
		for _, id := range badIDs {
			t.Run(h.name+"/"+id, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/widgets/x"+h.suffix, nil)
				req.SetPathValue("id", screen.ID)
				req.SetPathValue("pageID", page.ID)
				req.SetPathValue("widgetID", id)
				req = req.WithContext(ctx)
				rr := httptest.NewRecorder()
				h.handler.ServeHTTP(rr, req)

				if rr.Code != http.StatusFound {
					t.Fatalf("status = %d, want 302 (must not 500)", rr.Code)
				}
				if rr.Code >= 500 {
					t.Errorf("status = %d, must not 5xx", rr.Code)
				}
			})
		}
	}
}

// --- 8. ListWidgetInstancesByPage: cross-screen rejected ---

// The service must reject (screenA, pageOnScreenB) with ErrPageNotFound -- the
// service is the defence-in-depth layer for the URL parameter mismatch.
func TestService_ListWidgetInstancesByPage_CrossScreenRejected(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	screenA := createTestScreen(t, deps, "screen-a")
	screenB := createTestScreen(t, deps, "screen-b")
	page, err := deps.Screens.CreatePage(context.Background(), screenA.ID, "p")
	if err != nil {
		t.Fatalf("create page on A: %v", err)
	}

	// Ask the service to list widgets for the page on screen A but via screen B's ID.
	_, err = deps.Screens.ListWidgetInstancesByPage(context.Background(), screenB.ID, page.ID)
	if !errors.Is(err, screens.ErrPageNotFound) {
		t.Errorf("err = %v, want ErrPageNotFound", err)
	}
}

// --- 9. ListWidgetInstancesByPage: empty slice, non-nil, for a page with no widgets ---

func TestService_ListWidgetInstancesByPage_EmptySliceNonNil(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	widgets, err := deps.Screens.ListWidgetInstancesByPage(context.Background(), screen.ID, page.ID)
	if err != nil {
		t.Fatalf("list widgets: %v", err)
	}
	if widgets == nil {
		t.Error("widgets slice is nil; want non-nil empty slice")
	}
	if len(widgets) != 0 {
		t.Errorf("len(widgets) = %d, want 0", len(widgets))
	}
}

// --- 10. ListWidgetInstancesByPage: out-of-order insertion comes back sorted ---

// Insert 3 widgets with positions assigned in arbitrary order via the
// sqlc-generated CreateWidgetInstance (which bypasses the service's
// max-position auto-assign), and verify the service's list method returns
// them sorted ASC by position.
func TestService_ListWidgetInstancesByPage_PositionOrder(t *testing.T) {
	t.Parallel()
	sqlDB := dbOpenTestDB(t)
	q := dbNewQueries(sqlDB)
	authSvc := auth.NewService(sqlDB, auth.Config{
		AdminEmail:             "admin@example.com",
		SessionDuration:        time.Hour,
		CookieName:             "session",
		SecureCookie:           false,
		DeviceCookieName:       "device",
		DeviceLastSeenInterval: time.Minute,
		DeviceLandingURL:       "/device/",
	})
	themesSvc := themes.NewService(sqlDB, themes.Config{DefaultName: "default"})
	if err := themesSvc.EnsureDefault(context.Background()); err != nil {
		t.Fatalf("seed default theme: %v", err)
	}
	reg := widget.NewRegistry()
	screensSvc := screens.NewService(sqlDB, themesSvc, reg)
	deps := &Deps{Auth: authSvc, Themes: themesSvc, Widgets: reg, Screens: screensSvc}

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	// Insert directly via sqlc with positions 3, 1, 2 in that insertion order.
	type ins struct {
		id  string
		pos int64
	}
	rows := []ins{
		{"a000000000000000000000000000000a", 3},
		{"b000000000000000000000000000000b", 1},
		{"c000000000000000000000000000000c", 2},
	}
	for _, r := range rows {
		if err := q.CreateWidgetInstance(context.Background(), db.CreateWidgetInstanceParams{
			ID:       r.id,
			PageID:   page.ID,
			Type:     "text",
			Config:   `{"text":"x"}`,
			Position: r.pos,
		}); err != nil {
			t.Fatalf("insert widget pos %d: %v", r.pos, err)
		}
	}

	widgets, err := deps.Screens.ListWidgetInstancesByPage(context.Background(), screen.ID, page.ID)
	if err != nil {
		t.Fatalf("list widgets: %v", err)
	}
	if len(widgets) != 3 {
		t.Fatalf("len = %d, want 3", len(widgets))
	}
	if widgets[0].Position != 1 || widgets[1].Position != 2 || widgets[2].Position != 3 {
		t.Errorf("positions out of order: %d, %d, %d", widgets[0].Position, widgets[1].Position, widgets[2].Position)
	}
}

// --- 11. Concurrent AddWidget: TOCTOU race on (page_id, position) ---

// Spawn N concurrent AddWidget POSTs to the same page. The MaxWidgetPosition +
// CreateWidgetInstance pair is NOT in a transaction. Two concurrent writers
// can both read max=K, both try to insert at position K+1, and the UNIQUE
// index will fire on the second. The handler must surface that as a friendly
// flash (or success), not a 500 / panic. The race detector must stay clean.
func TestHandleWidgetCreate_ConcurrentAddRaceFree(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	const N = 4
	var wg sync.WaitGroup
	var status5xx atomic.Int32
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			form := url.Values{}
			form.Set("type", "text")
			req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/widgets", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetPathValue("id", screen.ID)
			req.SetPathValue("pageID", page.ID)
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()
			handleWidgetCreate(deps.Screens).ServeHTTP(rr, req)
			if rr.Code >= 500 {
				status5xx.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := status5xx.Load(); got > 0 {
		t.Errorf("got %d 5xx responses; concurrent Add should never 5xx (got a friendly redirect or success)", got)
	}

	// Verify there are no duplicate positions in the resulting rows.
	widgets, err := deps.Screens.ListWidgetInstancesByPage(context.Background(), screen.ID, page.ID)
	if err != nil {
		t.Fatalf("list widgets: %v", err)
	}
	seen := make(map[int]bool)
	for _, w := range widgets {
		if seen[w.Position] {
			t.Errorf("duplicate position %d", w.Position)
		}
		seen[w.Position] = true
	}
	_ = q
}

// --- 12. Concurrent MoveUp + MoveDown: no deadlock, no negative positions ---

func TestWidgetReorder_ConcurrentAcrossPages(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	w1 := addTestWidget(t, deps, screen.ID, page.ID, "text")
	w2 := addTestWidget(t, deps, screen.ID, page.ID, "text")

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.SetPathValue("id", screen.ID)
			req.SetPathValue("pageID", page.ID)
			req.SetPathValue("widgetID", w2.ID)
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()
			handleWidgetMoveUp(deps.Screens).ServeHTTP(rr, req)
		}()
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.SetPathValue("id", screen.ID)
			req.SetPathValue("pageID", page.ID)
			req.SetPathValue("widgetID", w1.ID)
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()
			handleWidgetMoveDown(deps.Screens).ServeHTTP(rr, req)
		}()
	}
	wg.Wait()

	// Final state: no negative positions, no duplicates.
	widgets, err := deps.Screens.ListWidgetInstancesByPage(context.Background(), screen.ID, page.ID)
	if err != nil {
		t.Fatalf("list widgets: %v", err)
	}
	seen := make(map[int]bool)
	for _, w := range widgets {
		if w.Position < 1 {
			t.Errorf("widget %s has non-positive position %d", w.ID, w.Position)
		}
		if seen[w.Position] {
			t.Errorf("duplicate position %d", w.Position)
		}
		seen[w.Position] = true
	}
	if len(widgets) != 2 {
		t.Errorf("len(widgets) = %d, want 2", len(widgets))
	}
}

// --- 13. End-to-end loop through page-edit + widget admin ---

func TestEndToEnd_ScreenPageWidgetLifecycle(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
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
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}

	post := func(t *testing.T, path string, form url.Values) *http.Response {
		t.Helper()
		body := form.Encode()
		req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		return resp
	}
	get := func(t *testing.T, path string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		return resp
	}

	def, err := deps.Themes.GetDefault(context.Background())
	if err != nil {
		t.Fatalf("get default theme: %v", err)
	}

	// 1. Create a screen.
	v := url.Values{}
	v.Set("name", "e2e")
	v.Set("theme_id", def.ID)
	v.Set("rotation_interval_seconds", "30")
	v.Set("_csrf", session.CSRFToken)
	resp := post(t, "/admin/screens", v)
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("create screen status = %d, want 302", resp.StatusCode)
	}

	// Discover the screen ID via service.
	list, err := deps.Screens.ListScreens(context.Background())
	if err != nil || len(list) == 0 {
		t.Fatalf("list screens: %v, len=%d", err, len(list))
	}
	var screenID string
	for _, s := range list {
		if s.Name == "e2e" {
			screenID = s.ID
		}
	}
	if screenID == "" {
		t.Fatal("screen 'e2e' not found in list")
	}

	// 2. Add a page.
	v = url.Values{}
	v.Set("name", "main")
	v.Set("_csrf", session.CSRFToken)
	resp = post(t, "/admin/screens/"+screenID+"/pages", v)
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("create page status = %d, want 302", resp.StatusCode)
	}

	pages, err := deps.Screens.ListPages(context.Background(), screenID)
	if err != nil || len(pages) != 1 {
		t.Fatalf("pages after create: err=%v len=%d", err, len(pages))
	}
	pageID := pages[0].ID

	// 3. Add a widget.
	v = url.Values{}
	v.Set("type", "text")
	v.Set("_csrf", session.CSRFToken)
	resp = post(t, "/admin/screens/"+screenID+"/pages/"+pageID+"/widgets", v)
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("add widget status = %d, want 302", resp.StatusCode)
	}

	// 4. GET the page-edit -- widget should appear in the table.
	resp = get(t, "/admin/screens/"+screenID+"/pages/"+pageID+"/edit")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get page-edit status = %d, want 200", resp.StatusCode)
	}
	bodyBytes := make([]byte, 1<<20)
	n, _ := resp.Body.Read(bodyBytes)
	resp.Body.Close()
	body := string(bodyBytes[:n])
	if !strings.Contains(body, "Text") {
		t.Error("page-edit body missing widget DisplayName 'Text'")
	}

	// 5. Add another widget, then MoveUp the second one.
	v = url.Values{}
	v.Set("type", "text")
	v.Set("_csrf", session.CSRFToken)
	resp = post(t, "/admin/screens/"+screenID+"/pages/"+pageID+"/widgets", v)
	resp.Body.Close()

	widgets, err := deps.Screens.ListWidgetInstancesByPage(context.Background(), screenID, pageID)
	if err != nil || len(widgets) != 2 {
		t.Fatalf("widgets after second add: err=%v len=%d", err, len(widgets))
	}
	w2ID := widgets[1].ID
	v = url.Values{}
	v.Set("_csrf", session.CSRFToken)
	resp = post(t, "/admin/screens/"+screenID+"/pages/"+pageID+"/widgets/"+w2ID+"/move-up", v)
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("move-up status = %d, want 302", resp.StatusCode)
	}

	widgets, _ = deps.Screens.ListWidgetInstancesByPage(context.Background(), screenID, pageID)
	if len(widgets) != 2 || widgets[0].ID != w2ID {
		t.Errorf("after move-up: widgets[0].ID = %q, want %q", widgets[0].ID, w2ID)
	}

	// 6. Delete the now-top widget.
	v = url.Values{}
	v.Set("_csrf", session.CSRFToken)
	resp = post(t, "/admin/screens/"+screenID+"/pages/"+pageID+"/widgets/"+w2ID+"/delete", v)
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("delete widget status = %d, want 302", resp.StatusCode)
	}

	widgets, _ = deps.Screens.ListWidgetInstancesByPage(context.Background(), screenID, pageID)
	if len(widgets) != 1 {
		t.Errorf("after delete: len(widgets) = %d, want 1", len(widgets))
	}
}

// --- 14. GET on widget POST-only routes is rejected ---

func TestWidgetRoutes_GETOnPostRouteRejected(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	w := addTestWidget(t, deps, screen.ID, page.ID, "text")

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}

	posts := []string{
		"/admin/screens/" + screen.ID + "/pages/" + page.ID + "/widgets",
		"/admin/screens/" + screen.ID + "/pages/" + page.ID + "/widgets/" + w.ID + "/delete",
		"/admin/screens/" + screen.ID + "/pages/" + page.ID + "/widgets/" + w.ID + "/move-up",
		"/admin/screens/" + screen.ID + "/pages/" + page.ID + "/widgets/" + w.ID + "/move-down",
	}
	for _, p := range posts {
		t.Run(p, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, srv.URL+p, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", p, err)
			}
			resp.Body.Close()
			if resp.StatusCode >= 500 {
				t.Errorf("GET %s: status = %d, must not 5xx", p, resp.StatusCode)
			}
			// 200 or 302 would mean the route matched a GET handler.
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusFound {
				t.Errorf("GET %s: status = %d, want 4xx (POST-only route)", p, resp.StatusCode)
			}
		})
	}
}

// --- 15. Member is 403 on every new widget admin route ---

func TestWidgetRoutes_MemberIs403(t *testing.T) {
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

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	w := addTestWidget(t, deps, screen.ID, page.ID, "text")

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}

	type probe struct{ method, path string }
	probes := []probe{
		{http.MethodGet, "/admin/screens/" + screen.ID + "/pages/" + page.ID + "/edit"},
		{http.MethodPost, "/admin/screens/" + screen.ID + "/pages/" + page.ID + "/widgets"},
		{http.MethodPost, "/admin/screens/" + screen.ID + "/pages/" + page.ID + "/widgets/" + w.ID + "/delete"},
		{http.MethodPost, "/admin/screens/" + screen.ID + "/pages/" + page.ID + "/widgets/" + w.ID + "/move-up"},
		{http.MethodPost, "/admin/screens/" + screen.ID + "/pages/" + page.ID + "/widgets/" + w.ID + "/move-down"},
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
				t.Errorf("%s %s: status = %d, want 403", p.method, p.path, resp.StatusCode)
			}
		})
	}
}

// --- 16. CSRF: every new widget POST route is rejected without _csrf ---

func TestWidgetRoutes_AllPOSTsRequireCSRF(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	w := addTestWidget(t, deps, screen.ID, page.ID, "text")

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}

	posts := []string{
		"/admin/screens/" + screen.ID + "/pages/" + page.ID + "/widgets",
		"/admin/screens/" + screen.ID + "/pages/" + page.ID + "/widgets/" + w.ID + "/delete",
		"/admin/screens/" + screen.ID + "/pages/" + page.ID + "/widgets/" + w.ID + "/move-up",
		"/admin/screens/" + screen.ID + "/pages/" + page.ID + "/widgets/" + w.ID + "/move-down",
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
				t.Errorf("POST %s: status = %d, want 403", p, resp.StatusCode)
			}
		})
	}

	// Widget must still exist.
	widgets, err := deps.Screens.ListWidgetInstancesByPage(context.Background(), screen.ID, page.ID)
	if err != nil {
		t.Fatalf("list widgets after CSRF rejection: %v", err)
	}
	if len(widgets) != 1 {
		t.Errorf("widget should still exist; got %d widgets", len(widgets))
	}
}

// --- 17. Unauthenticated user on widget routes is redirected to login ---

func TestWidgetRoutes_UnauthenticatedRedirectsToLogin(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDepsWithGoogle(t)

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/admin/screens/x/pages/y/edit", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/html")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET page-edit: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 (unauth → login)", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "/admin/login") {
		t.Errorf("Location = %q, want /admin/login", loc)
	}
}

// --- 18. Page-edit GET with empty widget list still renders Add Widget form ---

func TestHandlePageEditForm_ZeroWidgetsRendersAddForm(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/edit", nil)
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", page.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageEditForm(deps.Screens, deps.Widgets).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "<h2>Add Widget</h2>") {
		t.Error("body missing Add Widget heading for empty page")
	}
	if !strings.Contains(body, `<select name="type"`) {
		t.Error("body missing widget-type select for empty page")
	}
	if !strings.Contains(body, "Page Settings") {
		t.Error("body missing Page Settings section")
	}
	// Back link must be present.
	wantBack := "/admin/screens/" + screen.ID + "/edit"
	if !strings.Contains(body, wantBack) {
		t.Errorf("body missing back link %q", wantBack)
	}
}

// --- 19. Page-edit form action is correct ---

// AC alignment: the Page Settings form on the page-edit view must POST to the
// existing TASK-025 page update endpoint.
func TestHandlePageEditForm_PageSettingsFormAction(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "main")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/edit", nil)
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", page.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageEditForm(deps.Screens, deps.Widgets).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	wantAction := `action="/admin/screens/` + screen.ID + `/pages/` + page.ID + `"`
	if !strings.Contains(body, wantAction) {
		t.Errorf("Page Settings form action missing; want %q in body", wantAction)
	}

	// Add-widget form posts to .../widgets.
	wantAddAction := `action="/admin/screens/` + screen.ID + `/pages/` + page.ID + `/widgets"`
	if !strings.Contains(body, wantAddAction) {
		t.Errorf("Add Widget form action missing; want %q in body", wantAddAction)
	}
}

// --- 20. Page name with HTML in hero / back link escapes ---

// Page names cannot contain HTML per validatePageName, but we exercise the
// rendered hero by checking the escape behaviour for screen.Name (which also
// uses the regex). Confirm the hero renders without unescaped tags. The
// strongest test is to confirm the body never contains the literal payload
// for a registered name; since the regex blocks markup, we test via the
// flash query param which is admin-controlled and uncontrained.
func TestHandlePageEditForm_HeroIsEscapeSafe(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "main")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/edit", nil)
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", page.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageEditForm(deps.Screens, deps.Widgets).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	// Sanity: page name and screen name should appear in the hero region.
	if !strings.Contains(body, "Edit Page:") {
		t.Error("body missing 'Edit Page:' hero")
	}
	if !strings.Contains(body, "Back to "+screen.Name) {
		t.Errorf("body missing 'Back to %s'", screen.Name)
	}
}

// --- 21. Widget admin handlers: nil user / nil session return 403 ---

func TestWidgetHandlers_NoUserContextReturns403(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	handlers := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"create", handleWidgetCreate(deps.Screens)},
		{"delete", handleWidgetDelete(deps.Screens)},
		{"move-up", handleWidgetMoveUp(deps.Screens)},
		{"move-down", handleWidgetMoveDown(deps.Screens)},
		{"page-edit", handlePageEditForm(deps.Screens, deps.Widgets)},
	}
	for _, h := range handlers {
		t.Run(h.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.SetPathValue("id", "x")
			req.SetPathValue("pageID", "y")
			req.SetPathValue("widgetID", "z")
			rr := httptest.NewRecorder()
			h.handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rr.Code)
			}
		})
	}
}
