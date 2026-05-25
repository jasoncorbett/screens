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
	"github.com/jasoncorbett/screens/internal/screens"
	"github.com/jasoncorbett/screens/internal/widget"
	"github.com/jasoncorbett/screens/internal/widget/text"
)

// addTestWidget seeds a widget instance on the given page using the service.
func addTestWidget(t *testing.T, deps *Deps, screenID, pageID, widgetType string) screens.WidgetInstance {
	t.Helper()
	wi, err := deps.Screens.AddWidget(context.Background(), screenID, pageID, widgetType)
	if err != nil {
		t.Fatalf("add widget: %v", err)
	}
	return wi
}

func TestHandlePageEditForm_RendersWidgetList(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "main")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	addTestWidget(t, deps, screen.ID, page.ID, "text")

	req := httptest.NewRequest(http.MethodGet, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/edit", nil)
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", page.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageEditForm(deps.Screens, deps.Widgets).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()

	// AC-24: the widget's DisplayName, its position, and its config JSON.
	if !strings.Contains(body, "Text") {
		t.Error("body missing widget DisplayName 'Text'")
	}
	// Position number 1 should appear in the widgets table row.
	widgetsStart := strings.Index(body, "<h2>Widgets</h2>")
	widgetsEnd := strings.Index(body, "<h2>Add Widget</h2>")
	if widgetsStart < 0 || widgetsEnd < 0 || widgetsEnd <= widgetsStart {
		t.Fatal("body missing Widgets section markers")
	}
	widgetsSection := body[widgetsStart:widgetsEnd]
	if !strings.Contains(widgetsSection, ">1<") {
		t.Errorf("Widgets section missing position '1'; section=%q", widgetsSection)
	}
	if !strings.Contains(body, "<pre>") || !strings.Contains(body, "</pre>") {
		t.Error("body missing <pre> block for widget config")
	}
	if !strings.Contains(body, `&#34;text&#34;:&#34;Hello, screens&#34;`) {
		// templ HTML-escapes quotes; check the escaped form of {"text":"Hello, screens"}.
		t.Errorf("body missing escaped JSON config; got=%q", body)
	}
}

func TestHandlePageEditForm_ShowsWidgetTypeSelect(t *testing.T) {
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
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()

	// AC-25: <select name="type"> populated with the registered widget types.
	if !strings.Contains(body, `<select name="type"`) {
		t.Error(`body missing <select name="type">`)
	}
	if !strings.Contains(body, `value="text"`) {
		t.Errorf("body missing option with value=\"text\"; body=%q", body)
	}
}

func TestHandlePageEditForm_UnknownScreen(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	req := httptest.NewRequest(http.MethodGet, "/admin/screens/nope/pages/whatever/edit", nil)
	req.SetPathValue("id", "nope")
	req.SetPathValue("pageID", "whatever")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageEditForm(deps.Screens, deps.Widgets).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/screens?error=Screen+not+found" {
		t.Errorf("Location = %q, want /admin/screens?error=Screen+not+found", loc)
	}
}

func TestHandlePageEditForm_UnknownPage(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")

	req := httptest.NewRequest(http.MethodGet, "/admin/screens/"+screen.ID+"/pages/missing/edit", nil)
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", "missing")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageEditForm(deps.Screens, deps.Widgets).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	wantLoc := "/admin/screens/" + screen.ID + "/edit?error=Page+not+found"
	if loc := rr.Header().Get("Location"); loc != wantLoc {
		t.Errorf("Location = %q, want %q", loc, wantLoc)
	}
}

func TestHandleWidgetCreate_HappyPath(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	form := url.Values{}
	form.Set("type", "text")
	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/widgets", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", page.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleWidgetCreate(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	wantLoc := "/admin/screens/" + screen.ID + "/pages/" + page.ID + "/edit?msg=widget_added"
	if loc := rr.Header().Get("Location"); loc != wantLoc {
		t.Errorf("Location = %q, want %q", loc, wantLoc)
	}

	widgets, err := deps.Screens.ListWidgetInstancesByPage(context.Background(), screen.ID, page.ID)
	if err != nil {
		t.Fatalf("list widgets: %v", err)
	}
	if len(widgets) != 1 {
		t.Fatalf("len(widgets) = %d, want 1", len(widgets))
	}
	if widgets[0].Type != "text" {
		t.Errorf("widget type = %q, want text", widgets[0].Type)
	}
	if widgets[0].Position != 1 {
		t.Errorf("widget position = %d, want 1", widgets[0].Position)
	}
}

func TestHandleWidgetCreate_RejectsUnknownType(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	form := url.Values{}
	form.Set("type", "nope")
	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/widgets", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", page.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleWidgetCreate(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	wantLoc := "/admin/screens/" + screen.ID + "/pages/" + page.ID + "/edit?error=Unknown+widget+type"
	if loc := rr.Header().Get("Location"); loc != wantLoc {
		t.Errorf("Location = %q, want %q", loc, wantLoc)
	}

	widgets, err := deps.Screens.ListWidgetInstancesByPage(context.Background(), screen.ID, page.ID)
	if err != nil {
		t.Fatalf("list widgets: %v", err)
	}
	if len(widgets) != 0 {
		t.Errorf("len(widgets) = %d, want 0", len(widgets))
	}
}

func TestHandleWidgetCreate_RejectsMissingType(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/widgets", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", page.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleWidgetCreate(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	wantLoc := "/admin/screens/" + screen.ID + "/pages/" + page.ID + "/edit?error=Widget+type+is+required"
	if loc := rr.Header().Get("Location"); loc != wantLoc {
		t.Errorf("Location = %q, want %q", loc, wantLoc)
	}
}

func TestHandleWidgetDelete_HappyPath(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	wi := addTestWidget(t, deps, screen.ID, page.ID, "text")

	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/widgets/"+wi.ID+"/delete", nil)
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", page.ID)
	req.SetPathValue("widgetID", wi.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleWidgetDelete(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	wantLoc := "/admin/screens/" + screen.ID + "/pages/" + page.ID + "/edit?msg=widget_deleted"
	if loc := rr.Header().Get("Location"); loc != wantLoc {
		t.Errorf("Location = %q, want %q", loc, wantLoc)
	}

	widgets, err := deps.Screens.ListWidgetInstancesByPage(context.Background(), screen.ID, page.ID)
	if err != nil {
		t.Fatalf("list widgets: %v", err)
	}
	if len(widgets) != 0 {
		t.Errorf("len(widgets) = %d, want 0", len(widgets))
	}
}

func TestHandleWidgetDelete_UnknownWidget(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/widgets/ghost/delete", nil)
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", page.ID)
	req.SetPathValue("widgetID", "ghost")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleWidgetDelete(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	wantLoc := "/admin/screens/" + screen.ID + "/pages/" + page.ID + "/edit?error=Widget+not+found"
	if loc := rr.Header().Get("Location"); loc != wantLoc {
		t.Errorf("Location = %q, want %q", loc, wantLoc)
	}
}

func TestHandleWidgetMoveUp_Swaps(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	w1 := addTestWidget(t, deps, screen.ID, page.ID, "text")
	w2 := addTestWidget(t, deps, screen.ID, page.ID, "text")

	// w1 should be position 1, w2 should be position 2; move w2 up.
	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/widgets/"+w2.ID+"/move-up", nil)
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", page.ID)
	req.SetPathValue("widgetID", w2.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleWidgetMoveUp(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	wantLoc := "/admin/screens/" + screen.ID + "/pages/" + page.ID + "/edit?msg=widget_moved"
	if loc := rr.Header().Get("Location"); loc != wantLoc {
		t.Errorf("Location = %q, want %q", loc, wantLoc)
	}

	widgets, err := deps.Screens.ListWidgetInstancesByPage(context.Background(), screen.ID, page.ID)
	if err != nil {
		t.Fatalf("list widgets: %v", err)
	}
	if len(widgets) != 2 {
		t.Fatalf("len(widgets) = %d, want 2", len(widgets))
	}
	if widgets[0].ID != w2.ID {
		t.Errorf("widgets[0].ID = %q, want w2 (%q)", widgets[0].ID, w2.ID)
	}
	if widgets[1].ID != w1.ID {
		t.Errorf("widgets[1].ID = %q, want w1 (%q)", widgets[1].ID, w1.ID)
	}
}

func TestHandleWidgetMoveUp_AtTopIsNoOp(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	w1 := addTestWidget(t, deps, screen.ID, page.ID, "text")
	w2 := addTestWidget(t, deps, screen.ID, page.ID, "text")

	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/widgets/"+w1.ID+"/move-up", nil)
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", page.ID)
	req.SetPathValue("widgetID", w1.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleWidgetMoveUp(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	wantLoc := "/admin/screens/" + screen.ID + "/pages/" + page.ID + "/edit?msg=widget_moved"
	if loc := rr.Header().Get("Location"); loc != wantLoc {
		t.Errorf("Location = %q, want %q", loc, wantLoc)
	}

	widgets, err := deps.Screens.ListWidgetInstancesByPage(context.Background(), screen.ID, page.ID)
	if err != nil {
		t.Fatalf("list widgets: %v", err)
	}
	if widgets[0].ID != w1.ID || widgets[0].Position != 1 {
		t.Errorf("widgets[0] = %+v, want w1 at position 1", widgets[0])
	}
	if widgets[1].ID != w2.ID || widgets[1].Position != 2 {
		t.Errorf("widgets[1] = %+v, want w2 at position 2", widgets[1])
	}
}

// TestHandleWidgetCreate_DefaultConfigValidates is AC-20: the persisted config
// bytes after a widget-create handler call pass widget.Default().Validate.
func TestHandleWidgetCreate_DefaultConfigValidates(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	form := url.Values{}
	form.Set("type", "text")
	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/widgets", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", page.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleWidgetCreate(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}

	widgets, err := deps.Screens.ListWidgetInstancesByPage(context.Background(), screen.ID, page.ID)
	if err != nil {
		t.Fatalf("list widgets: %v", err)
	}
	if len(widgets) != 1 {
		t.Fatalf("len(widgets) = %d, want 1", len(widgets))
	}

	// Build a fresh registry with text to validate the persisted config.
	reg := widget.NewRegistry()
	if err := reg.Register(text.Registration()); err != nil {
		t.Fatalf("register text widget: %v", err)
	}
	if _, err := reg.Validate("text", widgets[0].Config); err != nil {
		t.Errorf("validate persisted config: %v", err)
	}
}

// TestWidgetDeleteRoute_CSRFRequired runs the full admin mux and verifies
// that a POST without _csrf is rejected, leaving the widget row intact.
func TestWidgetDeleteRoute_CSRFRequired(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	wi := addTestWidget(t, deps, screen.ID, page.ID, "text")

	mux := http.NewServeMux()
	AddRoutes(mux, deps)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/screens/"+screen.ID+"/pages/"+page.ID+"/widgets/"+wi.ID+"/delete", nil)
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
		t.Fatalf("status = %d, want %d (CSRF middleware should reject missing _csrf)", resp.StatusCode, http.StatusForbidden)
	}

	widgets, err := deps.Screens.ListWidgetInstancesByPage(context.Background(), screen.ID, page.ID)
	if err != nil {
		t.Fatalf("list widgets after CSRF rejection: %v", err)
	}
	if len(widgets) != 1 {
		t.Errorf("widget should still exist after CSRF rejection; got %d widgets", len(widgets))
	}
}

// TestGetScreenFull_EndToEnd builds a screen + 2 pages + 3 widgets purely
// through the service / handler APIs and verifies the assembled tree.
func TestGetScreenFull_EndToEnd(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})
	_ = ctx

	screen := createTestScreen(t, deps, "kitchen")

	p1, err := deps.Screens.CreatePage(context.Background(), screen.ID, "first")
	if err != nil {
		t.Fatalf("create p1: %v", err)
	}
	p2, err := deps.Screens.CreatePage(context.Background(), screen.ID, "second")
	if err != nil {
		t.Fatalf("create p2: %v", err)
	}

	w1a := addTestWidget(t, deps, screen.ID, p1.ID, "text")
	w1b := addTestWidget(t, deps, screen.ID, p1.ID, "text")
	w2 := addTestWidget(t, deps, screen.ID, p2.ID, "text")

	full, err := deps.Screens.GetScreenFull(context.Background(), screen.ID)
	if err != nil {
		t.Fatalf("get screen full: %v", err)
	}
	if full.Screen.ID != screen.ID {
		t.Errorf("Screen.ID = %q, want %q", full.Screen.ID, screen.ID)
	}
	if len(full.Pages) != 2 {
		t.Fatalf("len(Pages) = %d, want 2", len(full.Pages))
	}
	if full.Pages[0].Page.ID != p1.ID {
		t.Errorf("Pages[0].Page.ID = %q, want p1 (%q)", full.Pages[0].Page.ID, p1.ID)
	}
	if full.Pages[1].Page.ID != p2.ID {
		t.Errorf("Pages[1].Page.ID = %q, want p2 (%q)", full.Pages[1].Page.ID, p2.ID)
	}
	if len(full.Pages[0].Widgets) != 2 {
		t.Fatalf("Pages[0] widget count = %d, want 2", len(full.Pages[0].Widgets))
	}
	if full.Pages[0].Widgets[0].ID != w1a.ID {
		t.Errorf("Pages[0].Widgets[0].ID = %q, want w1a (%q)", full.Pages[0].Widgets[0].ID, w1a.ID)
	}
	if full.Pages[0].Widgets[1].ID != w1b.ID {
		t.Errorf("Pages[0].Widgets[1].ID = %q, want w1b (%q)", full.Pages[0].Widgets[1].ID, w1b.ID)
	}
	if len(full.Pages[1].Widgets) != 1 {
		t.Fatalf("Pages[1] widget count = %d, want 1", len(full.Pages[1].Widgets))
	}
	if full.Pages[1].Widgets[0].ID != w2.ID {
		t.Errorf("Pages[1].Widgets[0].ID = %q, want w2 (%q)", full.Pages[1].Widgets[0].ID, w2.ID)
	}
}

// TestScreenMsgText_WidgetCodes covers the three new flash codes added by
// this task.
func TestScreenMsgText_WidgetCodes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code string
		want string
	}{
		{"widget_added", "Widget added."},
		{"widget_deleted", "Widget removed."},
		{"widget_moved", "Widget reordered."},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			if got := screenMsgText(tt.code); got != tt.want {
				t.Errorf("screenMsgText(%q) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}

// TestWidgetDisplayName_FallsBackToType verifies that the helper returns the
// raw type when no registration matches.
func TestWidgetDisplayName_FallsBackToType(t *testing.T) {
	t.Parallel()
	regs := []widget.Registration{
		{Type: "text", DisplayName: "Text"},
	}
	if got := widgetDisplayName(regs, "text"); got != "Text" {
		t.Errorf("widgetDisplayName(regs, text) = %q, want Text", got)
	}
	if got := widgetDisplayName(regs, "unknown"); got != "unknown" {
		t.Errorf("widgetDisplayName(regs, unknown) = %q, want unknown (fallback)", got)
	}
	if got := widgetDisplayName(nil, "anything"); got != "anything" {
		t.Errorf("widgetDisplayName(nil, anything) = %q, want anything", got)
	}
}

// TestService_ListWidgetInstancesByPage_UnknownPage verifies the service
// surfaces ErrPageNotFound for an unknown page.
func TestService_ListWidgetInstancesByPage_UnknownPage(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	screen := createTestScreen(t, deps, "kitchen")
	_, err := deps.Screens.ListWidgetInstancesByPage(context.Background(), screen.ID, "nope")
	if !errors.Is(err, screens.ErrPageNotFound) {
		t.Errorf("err = %v, want ErrPageNotFound", err)
	}
}
