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
	"github.com/jasoncorbett/screens/internal/themes"
)

// createTestScreen seeds a screen using the default theme and returns it.
func createTestScreen(t *testing.T, deps *Deps, name string) screens.Screen {
	t.Helper()
	def, err := deps.Themes.GetDefault(context.Background())
	if err != nil {
		t.Fatalf("get default theme: %v", err)
	}
	s, err := deps.Screens.CreateScreen(context.Background(), screens.ScreenInput{
		Name:                    name,
		ThemeID:                 def.ID,
		RotationIntervalSeconds: 30,
	})
	if err != nil {
		t.Fatalf("create screen %q: %v", name, err)
	}
	return s
}

func validScreenForm(t *testing.T, deps *Deps, name string) url.Values {
	t.Helper()
	def, err := deps.Themes.GetDefault(context.Background())
	if err != nil {
		t.Fatalf("get default theme: %v", err)
	}
	form := url.Values{}
	form.Set("name", name)
	form.Set("theme_id", def.ID)
	form.Set("rotation_interval_seconds", "30")
	return form
}

func TestHandleScreenList_RendersScreens(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	created := createTestScreen(t, deps, "kitchen")

	req := httptest.NewRequest(http.MethodGet, "/admin/screens", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenList(deps.Screens, deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "kitchen") {
		t.Error("body missing screen name 'kitchen'")
	}
	if !strings.Contains(body, "default") {
		t.Error("body missing theme name 'default'")
	}
	if !strings.Contains(body, "Screen Management") {
		t.Error("body missing 'Screen Management' header")
	}
	if !strings.Contains(body, ">0<") && !strings.Contains(body, "0</td>") {
		// Page count column for a fresh screen should show 0.
		t.Errorf("body missing page count 0 for fresh screen; got %q", body)
	}
	_ = created
}

func TestHandleScreenList_NoUserContext_Returns403(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/screens", nil)
	rr := httptest.NewRecorder()
	handleScreenList(deps.Screens, deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestHandleScreenCreate_HappyPath(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	form := validScreenForm(t, deps, "kitchen")
	req := httptest.NewRequest(http.MethodPost, "/admin/screens", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenCreate(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/screens?msg=created" {
		t.Errorf("Location = %q, want /admin/screens?msg=created", loc)
	}

	list, err := deps.Screens.ListScreens(context.Background())
	if err != nil {
		t.Fatalf("list screens: %v", err)
	}
	found := false
	for _, s := range list {
		if s.Name == "kitchen" {
			found = true
		}
	}
	if !found {
		t.Fatal("created screen 'kitchen' not in list")
	}
}

func TestHandleScreenCreate_RejectsEmptyName(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	baseline, err := deps.Screens.ListScreens(context.Background())
	if err != nil {
		t.Fatalf("baseline list: %v", err)
	}

	form := validScreenForm(t, deps, "")
	req := httptest.NewRequest(http.MethodPost, "/admin/screens", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenCreate(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "/admin/screens?error=") {
		t.Errorf("Location = %q, want redirect with error param", loc)
	}
	if !strings.Contains(strings.ToLower(loc), "name") {
		t.Errorf("Location = %q, want error message mentioning name", loc)
	}

	after, err := deps.Screens.ListScreens(context.Background())
	if err != nil {
		t.Fatalf("after list: %v", err)
	}
	if len(after) != len(baseline) {
		t.Errorf("list size changed; got %d, want %d", len(after), len(baseline))
	}
}

func TestHandleScreenCreate_RejectsInvalidThemeID(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	baseline, err := deps.Screens.ListScreens(context.Background())
	if err != nil {
		t.Fatalf("baseline list: %v", err)
	}

	form := url.Values{}
	form.Set("name", "kitchen")
	form.Set("theme_id", "does-not-exist")
	form.Set("rotation_interval_seconds", "30")

	req := httptest.NewRequest(http.MethodPost, "/admin/screens", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenCreate(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/screens?error=Theme+not+found" {
		t.Errorf("Location = %q, want /admin/screens?error=Theme+not+found", loc)
	}

	after, err := deps.Screens.ListScreens(context.Background())
	if err != nil {
		t.Fatalf("after list: %v", err)
	}
	if len(after) != len(baseline) {
		t.Errorf("list size changed; got %d, want %d", len(after), len(baseline))
	}
}

func TestHandleScreenCreate_RejectsOutOfRangeRotation(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	baseline, err := deps.Screens.ListScreens(context.Background())
	if err != nil {
		t.Fatalf("baseline list: %v", err)
	}

	form := validScreenForm(t, deps, "kitchen")
	form.Set("rotation_interval_seconds", "2")

	req := httptest.NewRequest(http.MethodPost, "/admin/screens", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenCreate(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "error=") {
		t.Errorf("Location = %q, want redirect with error param", loc)
	}

	after, err := deps.Screens.ListScreens(context.Background())
	if err != nil {
		t.Fatalf("after list: %v", err)
	}
	if len(after) != len(baseline) {
		t.Errorf("list size changed; got %d, want %d", len(after), len(baseline))
	}
}

func TestHandleScreenCreate_RejectsDuplicateName(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	createTestScreen(t, deps, "kitchen")

	form := validScreenForm(t, deps, "kitchen")
	req := httptest.NewRequest(http.MethodPost, "/admin/screens", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenCreate(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/screens?error=A+screen+with+that+name+already+exists" {
		t.Errorf("Location = %q, want duplicate-name error redirect", loc)
	}
}

func TestHandleScreenEditForm_PrePopulates(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	created := createTestScreen(t, deps, "lobby")

	req := httptest.NewRequest(http.MethodGet, "/admin/screens/"+created.ID+"/edit", nil)
	req.SetPathValue("id", created.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenEditForm(deps.Screens, deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `value="lobby"`) {
		t.Error("body missing value=\"lobby\" -- form should pre-populate the existing name")
	}
	// The default theme should be selected.
	def, err := deps.Themes.GetDefault(context.Background())
	if err != nil {
		t.Fatalf("get default theme: %v", err)
	}
	selectedFragment := `value="` + def.ID + `" selected`
	if !strings.Contains(body, selectedFragment) {
		t.Errorf("body missing selected option for default theme; want %q", selectedFragment)
	}
}

func TestHandleScreenEditForm_UnknownID(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	req := httptest.NewRequest(http.MethodGet, "/admin/screens/nonexistent/edit", nil)
	req.SetPathValue("id", "nonexistent")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenEditForm(deps.Screens, deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/screens?error=Screen+not+found" {
		t.Errorf("Location = %q, want /admin/screens?error=Screen+not+found", loc)
	}
}

func TestHandleScreenUpdate_HappyPath(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	created := createTestScreen(t, deps, "lobby")

	form := validScreenForm(t, deps, "lobby-renamed")
	form.Set("rotation_interval_seconds", "60")
	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+created.ID, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", created.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenUpdate(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/screens?msg=updated" {
		t.Errorf("Location = %q, want /admin/screens?msg=updated", loc)
	}

	got, err := deps.Screens.GetScreenByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get screen: %v", err)
	}
	if got.Name != "lobby-renamed" {
		t.Errorf("Name = %q, want lobby-renamed", got.Name)
	}
	if got.RotationIntervalSeconds != 60 {
		t.Errorf("RotationIntervalSeconds = %d, want 60", got.RotationIntervalSeconds)
	}
}

func TestHandleScreenUpdate_UnknownID(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	form := validScreenForm(t, deps, "irrelevant")
	req := httptest.NewRequest(http.MethodPost, "/admin/screens/nonexistent", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", "nonexistent")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenUpdate(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/screens?error=Screen+not+found" {
		t.Errorf("Location = %q, want /admin/screens?error=Screen+not+found", loc)
	}
}

func TestHandleScreenDelete_HappyPath(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	created := createTestScreen(t, deps, "to-delete")

	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+created.ID+"/delete", nil)
	req.SetPathValue("id", created.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenDelete(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/screens?msg=deleted" {
		t.Errorf("Location = %q, want /admin/screens?msg=deleted", loc)
	}

	if _, err := deps.Screens.GetScreenByID(context.Background(), created.ID); !errors.Is(err, screens.ErrScreenNotFound) {
		t.Errorf("GetScreenByID after delete err = %v, want ErrScreenNotFound", err)
	}
}

func TestHandlePageCreate_HappyPath(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")

	form := url.Values{}
	form.Set("name", "clock")
	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID+"/pages", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", screen.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageCreate(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	wantLoc := "/admin/screens/" + screen.ID + "/edit?msg=page_created"
	if loc := rr.Header().Get("Location"); loc != wantLoc {
		t.Errorf("Location = %q, want %q", loc, wantLoc)
	}

	pages, err := deps.Screens.ListPages(context.Background(), screen.ID)
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("len(pages) = %d, want 1", len(pages))
	}
	if pages[0].Name != "clock" {
		t.Errorf("page name = %q, want clock", pages[0].Name)
	}
}

func TestHandlePageMoveDown_Swaps(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	p1, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p1")
	if err != nil {
		t.Fatalf("create p1: %v", err)
	}
	p2, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p2")
	if err != nil {
		t.Fatalf("create p2: %v", err)
	}
	p3, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p3")
	if err != nil {
		t.Fatalf("create p3: %v", err)
	}

	// Move p1 (position 1) down.
	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID+"/pages/"+p1.ID+"/move-down", nil)
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", p1.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageMoveDown(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	wantLoc := "/admin/screens/" + screen.ID + "/edit?msg=page_moved"
	if loc := rr.Header().Get("Location"); loc != wantLoc {
		t.Errorf("Location = %q, want %q", loc, wantLoc)
	}

	pages, err := deps.Screens.ListPages(context.Background(), screen.ID)
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("len(pages) = %d, want 3", len(pages))
	}
	// Order should now be: p2, p1, p3.
	if pages[0].ID != p2.ID {
		t.Errorf("pages[0].ID = %q, want p2 (%q)", pages[0].ID, p2.ID)
	}
	if pages[1].ID != p1.ID {
		t.Errorf("pages[1].ID = %q, want p1 (%q)", pages[1].ID, p1.ID)
	}
	if pages[2].ID != p3.ID {
		t.Errorf("pages[2].ID = %q, want p3 (%q)", pages[2].ID, p3.ID)
	}
}

func TestHandlePageMoveUp_AtTopIsNoOp(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	p1, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p1")
	if err != nil {
		t.Fatalf("create p1: %v", err)
	}
	p2, err := deps.Screens.CreatePage(context.Background(), screen.ID, "p2")
	if err != nil {
		t.Fatalf("create p2: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID+"/pages/"+p1.ID+"/move-up", nil)
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", p1.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageMoveUp(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	wantLoc := "/admin/screens/" + screen.ID + "/edit?msg=page_moved"
	if loc := rr.Header().Get("Location"); loc != wantLoc {
		t.Errorf("Location = %q, want %q", loc, wantLoc)
	}

	pages, err := deps.Screens.ListPages(context.Background(), screen.ID)
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	// Positions unchanged: p1=1, p2=2.
	if pages[0].ID != p1.ID || pages[0].Position != 1 {
		t.Errorf("pages[0] = %+v, want p1 at position 1", pages[0])
	}
	if pages[1].ID != p2.ID || pages[1].Position != 2 {
		t.Errorf("pages[1] = %+v, want p2 at position 2", pages[1])
	}
}

func TestHandlePageDelete_CascadesWidgets(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	page, err := deps.Screens.CreatePage(context.Background(), screen.ID, "")
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	widget, err := deps.Screens.AddWidget(context.Background(), screen.ID, page.ID, "text")
	if err != nil {
		t.Fatalf("add widget: %v", err)
	}

	// Sanity: GetScreenFull should include the widget.
	full, err := deps.Screens.GetScreenFull(context.Background(), screen.ID)
	if err != nil {
		t.Fatalf("get screen full pre-delete: %v", err)
	}
	if len(full.Pages) != 1 || len(full.Pages[0].Widgets) != 1 {
		t.Fatalf("pre-delete state unexpected; widgets = %d", len(full.Pages[0].Widgets))
	}

	// Delete the page via handler.
	req := httptest.NewRequest(http.MethodPost, "/admin/screens/"+screen.ID+"/pages/"+page.ID+"/delete", nil)
	req.SetPathValue("id", screen.ID)
	req.SetPathValue("pageID", page.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handlePageDelete(deps.Screens).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}

	// Widget row should be gone (page CASCADE removed it).
	full, err = deps.Screens.GetScreenFull(context.Background(), screen.ID)
	if err != nil {
		t.Fatalf("get screen full post-delete: %v", err)
	}
	if len(full.Pages) != 0 {
		t.Fatalf("len(pages) = %d, want 0 after page delete", len(full.Pages))
	}
	_ = widget
}

func TestHandleScreenEditForm_ListsPagesInOrder(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	screen := createTestScreen(t, deps, "kitchen")
	p1, err := deps.Screens.CreatePage(context.Background(), screen.ID, "first")
	if err != nil {
		t.Fatalf("create p1: %v", err)
	}
	p2, err := deps.Screens.CreatePage(context.Background(), screen.ID, "second")
	if err != nil {
		t.Fatalf("create p2: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/screens/"+screen.ID+"/edit", nil)
	req.SetPathValue("id", screen.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleScreenEditForm(deps.Screens, deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	// Slice the body to the pages-table region so the search isn't fooled
	// by the substring "second" in "seconds" elsewhere in the form.
	pagesStart := strings.Index(body, "<h2>Pages</h2>")
	if pagesStart < 0 {
		t.Fatal("body missing '<h2>Pages</h2>' marker")
	}
	pagesEnd := strings.Index(body[pagesStart:], "<h2>New Page</h2>")
	if pagesEnd < 0 {
		t.Fatal("body missing '<h2>New Page</h2>' marker")
	}
	pagesSection := body[pagesStart : pagesStart+pagesEnd]

	if !strings.Contains(pagesSection, "first") {
		t.Error("pages section missing page name 'first'")
	}
	if !strings.Contains(pagesSection, "second") {
		t.Error("pages section missing page name 'second'")
	}
	firstIdx := strings.Index(pagesSection, "first")
	secondIdx := strings.Index(pagesSection, "second")
	if firstIdx >= secondIdx {
		t.Errorf("expected 'first' to appear before 'second' in pages section; firstIdx=%d secondIdx=%d", firstIdx, secondIdx)
	}
	_, _ = p1, p2
}

// TestScreenRoutes_MemberIs403 verifies a non-admin user is rejected from
// /admin/screens by RequireRole when the full middleware chain is wired.
func TestScreenRoutes_MemberIs403(t *testing.T) {
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

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/admin/screens", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/screens: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (member should be rejected by RequireRole)", resp.StatusCode, http.StatusForbidden)
	}
}

// TestScreenRoutes_CSRFRequired verifies the CSRF middleware rejects a POST
// to /admin/screens/{id}/delete without _csrf. The screen row must survive.
func TestScreenRoutes_CSRFRequired(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	screen := createTestScreen(t, deps, "doomed")

	mux := http.NewServeMux()
	AddRoutes(mux, deps)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/screens/"+screen.ID+"/delete", nil)
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

	if _, err := deps.Screens.GetScreenByID(context.Background(), screen.ID); err != nil {
		t.Errorf("screen should still exist after CSRF rejection; err = %v", err)
	}
}

// TestThemeDelete_InUseShowsFlash verifies the new ErrThemeInUse branch in
// handleThemeDelete redirects with the right error.
func TestThemeDelete_InUseShowsFlash(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")
	ctx := adminContext(t, deps, &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin})

	theme, err := deps.Themes.Create(context.Background(), themes.Input{
		Name:           "in-use-theme",
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

	// Create a screen referencing the theme.
	if _, err := deps.Screens.CreateScreen(context.Background(), screens.ScreenInput{
		Name:                    "kitchen",
		ThemeID:                 theme.ID,
		RotationIntervalSeconds: 30,
	}); err != nil {
		t.Fatalf("create screen: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/themes/"+theme.ID+"/delete", nil)
	req.SetPathValue("id", theme.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleThemeDelete(deps.Themes).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	wantLoc := "/admin/themes?error=Cannot+delete+a+theme+in+use+by+a+screen"
	if loc := rr.Header().Get("Location"); loc != wantLoc {
		t.Errorf("Location = %q, want %q", loc, wantLoc)
	}

	// Theme must still exist.
	if _, err := deps.Themes.GetByID(context.Background(), theme.ID); err != nil {
		t.Errorf("theme should still exist after rejected delete; err = %v", err)
	}
}

// TestAdminLandingPage_HasScreensLink verifies the admin landing page links
// to /admin/screens for admin users.
func TestAdminLandingPage_HasScreensLink(t *testing.T) {
	t.Parallel()

	user := &auth.User{
		ID:          "admin-id",
		Email:       "admin@example.com",
		DisplayName: "Admin",
		Role:        auth.RoleAdmin,
	}
	session := &auth.Session{CSRFToken: "csrf"}

	ctx := auth.ContextWithUser(context.Background(), user)
	ctx = auth.ContextWithSession(ctx, session)

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleAdmin(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `href="/admin/screens"`) {
		t.Error(`body missing href="/admin/screens" link`)
	}
}

// Sanity check on the screenMsgText helper to keep the flash code map honest.
func TestScreenMsgText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code string
		want string
	}{
		{"created", "Screen created."},
		{"updated", "Screen updated."},
		{"deleted", "Screen deleted."},
		{"page_created", "Page added."},
		{"page_updated", "Page updated."},
		{"page_deleted", "Page deleted."},
		{"page_moved", "Page reordered."},
		{"unknown", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			if got := screenMsgText(tt.code); got != tt.want {
				t.Errorf("screenMsgText(%q) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}
