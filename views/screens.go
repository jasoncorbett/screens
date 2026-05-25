package views

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/jasoncorbett/screens/internal/auth"
	"github.com/jasoncorbett/screens/internal/screens"
	"github.com/jasoncorbett/screens/internal/themes"
	"github.com/jasoncorbett/screens/internal/widget"
)

// screenMsgText maps a flash code to a user-visible status message. Returns an
// empty string for unknown codes; the templ skips the status card when the
// text is empty.
func screenMsgText(code string) string {
	switch code {
	case "created":
		return "Screen created."
	case "updated":
		return "Screen updated."
	case "deleted":
		return "Screen deleted."
	case "page_created":
		return "Page added."
	case "page_updated":
		return "Page updated."
	case "page_deleted":
		return "Page deleted."
	case "page_moved":
		return "Page reordered."
	case "widget_added":
		return "Widget added."
	case "widget_deleted":
		return "Widget removed."
	case "widget_moved":
		return "Widget reordered."
	default:
		return ""
	}
}

// widgetDisplayName returns the DisplayName for the widget type from the
// registrations slice, or the raw type string if no registration matches.
// The fallback covers the edge case where a widget type was deregistered
// but rows still exist in the database.
func widgetDisplayName(registrations []widget.Registration, typeName string) string {
	for _, reg := range registrations {
		if reg.Type == typeName {
			return reg.DisplayName
		}
	}
	return typeName
}

// screenInputFromForm extracts a screens.ScreenInput from the form values.
// Returns the input and any rotation parse error (empty/non-numeric value
// signaled separately so the handler can pick a friendly message).
func screenInputFromForm(r *http.Request) (screens.ScreenInput, error) {
	rotation, err := strconv.Atoi(r.FormValue("rotation_interval_seconds"))
	if err != nil {
		return screens.ScreenInput{
			Name:    r.FormValue("name"),
			ThemeID: r.FormValue("theme_id"),
		}, err
	}
	return screens.ScreenInput{
		Name:                    r.FormValue("name"),
		ThemeID:                 r.FormValue("theme_id"),
		RotationIntervalSeconds: rotation,
	}, nil
}

// firstValidationMessage returns a stable, human-readable message derived
// from a *screens.ValidationError. Fields are checked in alphabetical order
// so the surfaced message is deterministic.
func firstValidationMessage(ve *screens.ValidationError) string {
	if ve == nil || len(ve.Fields) == 0 {
		return "Invalid input"
	}
	keys := make([]string, 0, len(ve.Fields))
	for k := range ve.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	switch keys[0] {
	case "name":
		return "Invalid name"
	case "theme_id":
		return "Theme is required"
	case "rotation_interval_seconds":
		return "Rotation interval must be between 5 and 3600 seconds"
	default:
		return "Invalid input"
	}
}

func handleScreenList(svc *screens.Service, themesSvc *themes.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		session := auth.SessionFromContext(ctx)

		if user == nil || session == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		summaries, err := svc.ListScreens(ctx)
		if err != nil {
			slog.Error("list screens", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		themesList, err := themesSvc.List(ctx)
		if err != nil {
			slog.Error("list themes for screens list", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		msgCode := r.URL.Query().Get("msg")
		errMsg := r.URL.Query().Get("error")

		screensListPage(summaries, user, session.CSRFToken, screenMsgText(msgCode), errMsg, themesList).Render(ctx, w)
	}
}

func handleScreenCreate(svc *screens.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		session := auth.SessionFromContext(ctx)

		if user == nil || session == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		in, parseErr := screenInputFromForm(r)
		if parseErr != nil {
			http.Redirect(w, r, "/admin/screens?error=Invalid+rotation+interval", http.StatusFound)
			return
		}

		screen, err := svc.CreateScreen(ctx, in)
		if err != nil {
			var ve *screens.ValidationError
			if errors.As(err, &ve) {
				http.Redirect(w, r, "/admin/screens?error="+url.QueryEscape(firstValidationMessage(ve)), http.StatusFound)
				return
			}
			if errors.Is(err, screens.ErrDuplicateName) {
				http.Redirect(w, r, "/admin/screens?error=A+screen+with+that+name+already+exists", http.StatusFound)
				return
			}
			if errors.Is(err, screens.ErrThemeNotFound) {
				http.Redirect(w, r, "/admin/screens?error=Theme+not+found", http.StatusFound)
				return
			}
			slog.Error("create screen", "err", err)
			http.Redirect(w, r, "/admin/screens?error=Could+not+create+screen", http.StatusFound)
			return
		}

		slog.Info("screen created", "screen_id", screen.ID, "name", screen.Name, "created_by", user.Email)
		http.Redirect(w, r, "/admin/screens?msg=created", http.StatusFound)
	}
}

func handleScreenEditForm(svc *screens.Service, themesSvc *themes.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		session := auth.SessionFromContext(ctx)

		if user == nil || session == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		id := r.PathValue("id")
		if id == "" {
			http.Redirect(w, r, "/admin/screens?error=Missing+screen+ID", http.StatusFound)
			return
		}

		screen, err := svc.GetScreenByID(ctx, id)
		if err != nil {
			if errors.Is(err, screens.ErrScreenNotFound) {
				http.Redirect(w, r, "/admin/screens?error=Screen+not+found", http.StatusFound)
				return
			}
			slog.Error("get screen for edit", "err", err, "screen_id", id)
			http.Redirect(w, r, "/admin/screens?error=Could+not+load+screen", http.StatusFound)
			return
		}

		pages, err := svc.ListPages(ctx, id)
		if err != nil {
			slog.Error("list pages for edit", "err", err, "screen_id", id)
			http.Redirect(w, r, "/admin/screens?error=Could+not+load+screen", http.StatusFound)
			return
		}

		themesList, err := themesSvc.List(ctx)
		if err != nil {
			slog.Error("list themes for edit", "err", err)
			http.Redirect(w, r, "/admin/screens?error=Could+not+load+screen", http.StatusFound)
			return
		}

		msgCode := r.URL.Query().Get("msg")
		errMsg := r.URL.Query().Get("error")

		screenEditPage(screen, pages, user, session.CSRFToken, screenMsgText(msgCode), errMsg, themesList).Render(ctx, w)
	}
}

func handleScreenUpdate(svc *screens.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		session := auth.SessionFromContext(ctx)

		if user == nil || session == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		id := r.PathValue("id")
		if id == "" {
			http.Redirect(w, r, "/admin/screens?error=Missing+screen+ID", http.StatusFound)
			return
		}

		in, parseErr := screenInputFromForm(r)
		if parseErr != nil {
			http.Redirect(w, r, "/admin/screens/"+id+"/edit?error=Invalid+rotation+interval", http.StatusFound)
			return
		}

		_, err := svc.UpdateScreen(ctx, id, in)
		if err != nil {
			var ve *screens.ValidationError
			if errors.As(err, &ve) {
				http.Redirect(w, r, "/admin/screens/"+id+"/edit?error="+url.QueryEscape(firstValidationMessage(ve)), http.StatusFound)
				return
			}
			if errors.Is(err, screens.ErrScreenNotFound) {
				http.Redirect(w, r, "/admin/screens?error=Screen+not+found", http.StatusFound)
				return
			}
			if errors.Is(err, screens.ErrDuplicateName) {
				http.Redirect(w, r, "/admin/screens/"+id+"/edit?error=A+screen+with+that+name+already+exists", http.StatusFound)
				return
			}
			if errors.Is(err, screens.ErrThemeNotFound) {
				http.Redirect(w, r, "/admin/screens/"+id+"/edit?error=Theme+not+found", http.StatusFound)
				return
			}
			slog.Error("update screen", "err", err, "screen_id", id)
			http.Redirect(w, r, "/admin/screens/"+id+"/edit?error=Could+not+update+screen", http.StatusFound)
			return
		}

		slog.Info("screen updated", "screen_id", id, "updated_by", user.Email)
		http.Redirect(w, r, "/admin/screens?msg=updated", http.StatusFound)
	}
}

func handleScreenDelete(svc *screens.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		if user == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		id := r.PathValue("id")
		if id == "" {
			http.Redirect(w, r, "/admin/screens?error=Missing+screen+ID", http.StatusFound)
			return
		}

		if err := svc.DeleteScreen(ctx, id); err != nil {
			if errors.Is(err, screens.ErrScreenNotFound) {
				http.Redirect(w, r, "/admin/screens?error=Screen+not+found", http.StatusFound)
				return
			}
			slog.Error("delete screen", "err", err, "screen_id", id)
			http.Redirect(w, r, "/admin/screens?error=Could+not+delete+screen", http.StatusFound)
			return
		}

		slog.Info("screen deleted", "screen_id", id, "deleted_by", user.Email)
		http.Redirect(w, r, "/admin/screens?msg=deleted", http.StatusFound)
	}
}

func handlePageCreate(svc *screens.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		if user == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		screenID := r.PathValue("id")
		if screenID == "" {
			http.Redirect(w, r, "/admin/screens?error=Missing+screen+ID", http.StatusFound)
			return
		}

		name := r.FormValue("name")
		page, err := svc.CreatePage(ctx, screenID, name)
		if err != nil {
			if errors.Is(err, screens.ErrScreenNotFound) {
				http.Redirect(w, r, "/admin/screens?error=Screen+not+found", http.StatusFound)
				return
			}
			var ve *screens.ValidationError
			if errors.As(err, &ve) {
				http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error="+url.QueryEscape(firstValidationMessage(ve)), http.StatusFound)
				return
			}
			slog.Error("create page", "err", err, "screen_id", screenID)
			http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error=Could+not+add+page", http.StatusFound)
			return
		}

		slog.Info("page created", "screen_id", screenID, "page_id", page.ID, "created_by", user.Email)
		http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?msg=page_created", http.StatusFound)
	}
}

func handlePageUpdate(svc *screens.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		if user == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		screenID := r.PathValue("id")
		pageID := r.PathValue("pageID")
		if screenID == "" || pageID == "" {
			http.Redirect(w, r, "/admin/screens?error=Missing+page+ID", http.StatusFound)
			return
		}

		name := r.FormValue("name")
		_, err := svc.UpdatePage(ctx, screenID, pageID, name)
		if err != nil {
			if errors.Is(err, screens.ErrPageNotFound) {
				http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error=Page+not+found", http.StatusFound)
				return
			}
			var ve *screens.ValidationError
			if errors.As(err, &ve) {
				http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error="+url.QueryEscape(firstValidationMessage(ve)), http.StatusFound)
				return
			}
			slog.Error("update page", "err", err, "screen_id", screenID, "page_id", pageID)
			http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error=Could+not+update+page", http.StatusFound)
			return
		}

		slog.Info("page updated", "screen_id", screenID, "page_id", pageID, "updated_by", user.Email)
		http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?msg=page_updated", http.StatusFound)
	}
}

func handlePageDelete(svc *screens.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		if user == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		screenID := r.PathValue("id")
		pageID := r.PathValue("pageID")
		if screenID == "" || pageID == "" {
			http.Redirect(w, r, "/admin/screens?error=Missing+page+ID", http.StatusFound)
			return
		}

		if err := svc.DeletePage(ctx, screenID, pageID); err != nil {
			if errors.Is(err, screens.ErrPageNotFound) {
				http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error=Page+not+found", http.StatusFound)
				return
			}
			slog.Error("delete page", "err", err, "screen_id", screenID, "page_id", pageID)
			http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error=Could+not+delete+page", http.StatusFound)
			return
		}

		slog.Info("page deleted", "screen_id", screenID, "page_id", pageID, "deleted_by", user.Email)
		http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?msg=page_deleted", http.StatusFound)
	}
}

func handlePageMoveUp(svc *screens.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		if user == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		screenID := r.PathValue("id")
		pageID := r.PathValue("pageID")
		if screenID == "" || pageID == "" {
			http.Redirect(w, r, "/admin/screens?error=Missing+page+ID", http.StatusFound)
			return
		}

		if err := svc.MovePageUp(ctx, screenID, pageID); err != nil {
			if errors.Is(err, screens.ErrPageNotFound) {
				http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error=Page+not+found", http.StatusFound)
				return
			}
			slog.Error("move page up", "err", err, "screen_id", screenID, "page_id", pageID)
			http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error=Could+not+reorder+page", http.StatusFound)
			return
		}

		slog.Info("page moved up", "screen_id", screenID, "page_id", pageID, "moved_by", user.Email)
		http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?msg=page_moved", http.StatusFound)
	}
}

func handlePageMoveDown(svc *screens.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		if user == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		screenID := r.PathValue("id")
		pageID := r.PathValue("pageID")
		if screenID == "" || pageID == "" {
			http.Redirect(w, r, "/admin/screens?error=Missing+page+ID", http.StatusFound)
			return
		}

		if err := svc.MovePageDown(ctx, screenID, pageID); err != nil {
			if errors.Is(err, screens.ErrPageNotFound) {
				http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error=Page+not+found", http.StatusFound)
				return
			}
			slog.Error("move page down", "err", err, "screen_id", screenID, "page_id", pageID)
			http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error=Could+not+reorder+page", http.StatusFound)
			return
		}

		slog.Info("page moved down", "screen_id", screenID, "page_id", pageID, "moved_by", user.Email)
		http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?msg=page_moved", http.StatusFound)
	}
}

func handlePageEditForm(svc *screens.Service, registry *widget.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		session := auth.SessionFromContext(ctx)

		if user == nil || session == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		screenID := r.PathValue("id")
		pageID := r.PathValue("pageID")
		if screenID == "" || pageID == "" {
			http.Redirect(w, r, "/admin/screens?error=Missing+page+ID", http.StatusFound)
			return
		}

		screen, err := svc.GetScreenByID(ctx, screenID)
		if err != nil {
			if errors.Is(err, screens.ErrScreenNotFound) {
				http.Redirect(w, r, "/admin/screens?error=Screen+not+found", http.StatusFound)
				return
			}
			slog.Error("get screen for page edit", "err", err, "screen_id", screenID)
			http.Redirect(w, r, "/admin/screens?error=Could+not+load+screen", http.StatusFound)
			return
		}

		page, err := svc.GetPageByID(ctx, screenID, pageID)
		if err != nil {
			if errors.Is(err, screens.ErrPageNotFound) {
				http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error=Page+not+found", http.StatusFound)
				return
			}
			slog.Error("get page for edit", "err", err, "screen_id", screenID, "page_id", pageID)
			http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error=Could+not+load+page", http.StatusFound)
			return
		}

		widgets, err := svc.ListWidgetInstancesByPage(ctx, screenID, pageID)
		if err != nil {
			if errors.Is(err, screens.ErrPageNotFound) {
				http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error=Page+not+found", http.StatusFound)
				return
			}
			slog.Error("list widgets for page edit", "err", err, "screen_id", screenID, "page_id", pageID)
			http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error=Could+not+load+widgets", http.StatusFound)
			return
		}

		registrations := registry.List()

		msgCode := r.URL.Query().Get("msg")
		errMsg := r.URL.Query().Get("error")

		pageEditPage(screen, page, widgets, registrations, user, session.CSRFToken, screenMsgText(msgCode), errMsg).Render(ctx, w)
	}
}

func handleWidgetCreate(svc *screens.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		if user == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		screenID := r.PathValue("id")
		pageID := r.PathValue("pageID")
		if screenID == "" || pageID == "" {
			http.Redirect(w, r, "/admin/screens?error=Missing+page+ID", http.StatusFound)
			return
		}

		widgetType := r.FormValue("type")
		pageEditURL := "/admin/screens/" + screenID + "/pages/" + pageID + "/edit"
		if widgetType == "" {
			http.Redirect(w, r, pageEditURL+"?error=Widget+type+is+required", http.StatusFound)
			return
		}

		widget, err := svc.AddWidget(ctx, screenID, pageID, widgetType)
		if err != nil {
			if errors.Is(err, screens.ErrUnknownWidgetType) {
				http.Redirect(w, r, pageEditURL+"?error=Unknown+widget+type", http.StatusFound)
				return
			}
			if errors.Is(err, screens.ErrPageNotFound) {
				http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error=Page+not+found", http.StatusFound)
				return
			}
			if errors.Is(err, screens.ErrScreenNotFound) {
				http.Redirect(w, r, "/admin/screens?error=Screen+not+found", http.StatusFound)
				return
			}
			slog.Error("add widget", "err", err, "screen_id", screenID, "page_id", pageID, "type", widgetType)
			http.Redirect(w, r, pageEditURL+"?error=Could+not+add+widget", http.StatusFound)
			return
		}

		slog.Info("widget added", "screen_id", screenID, "page_id", pageID, "widget_id", widget.ID, "type", widget.Type, "added_by", user.Email)
		http.Redirect(w, r, pageEditURL+"?msg=widget_added", http.StatusFound)
	}
}

func handleWidgetDelete(svc *screens.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		if user == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		screenID := r.PathValue("id")
		pageID := r.PathValue("pageID")
		widgetID := r.PathValue("widgetID")
		if screenID == "" || pageID == "" || widgetID == "" {
			http.Redirect(w, r, "/admin/screens?error=Missing+widget+ID", http.StatusFound)
			return
		}

		pageEditURL := "/admin/screens/" + screenID + "/pages/" + pageID + "/edit"
		if err := svc.DeleteWidget(ctx, screenID, pageID, widgetID); err != nil {
			if errors.Is(err, screens.ErrWidgetNotFound) {
				http.Redirect(w, r, pageEditURL+"?error=Widget+not+found", http.StatusFound)
				return
			}
			if errors.Is(err, screens.ErrPageNotFound) {
				http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error=Page+not+found", http.StatusFound)
				return
			}
			if errors.Is(err, screens.ErrScreenNotFound) {
				http.Redirect(w, r, "/admin/screens?error=Screen+not+found", http.StatusFound)
				return
			}
			slog.Error("delete widget", "err", err, "screen_id", screenID, "page_id", pageID, "widget_id", widgetID)
			http.Redirect(w, r, pageEditURL+"?error=Could+not+delete+widget", http.StatusFound)
			return
		}

		slog.Info("widget deleted", "screen_id", screenID, "page_id", pageID, "widget_id", widgetID, "deleted_by", user.Email)
		http.Redirect(w, r, pageEditURL+"?msg=widget_deleted", http.StatusFound)
	}
}

func handleWidgetMoveUp(svc *screens.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		if user == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		screenID := r.PathValue("id")
		pageID := r.PathValue("pageID")
		widgetID := r.PathValue("widgetID")
		if screenID == "" || pageID == "" || widgetID == "" {
			http.Redirect(w, r, "/admin/screens?error=Missing+widget+ID", http.StatusFound)
			return
		}

		pageEditURL := "/admin/screens/" + screenID + "/pages/" + pageID + "/edit"
		if err := svc.MoveWidgetUp(ctx, screenID, pageID, widgetID); err != nil {
			if errors.Is(err, screens.ErrWidgetNotFound) {
				http.Redirect(w, r, pageEditURL+"?error=Widget+not+found", http.StatusFound)
				return
			}
			if errors.Is(err, screens.ErrPageNotFound) {
				http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error=Page+not+found", http.StatusFound)
				return
			}
			slog.Error("move widget up", "err", err, "screen_id", screenID, "page_id", pageID, "widget_id", widgetID)
			http.Redirect(w, r, pageEditURL+"?error=Could+not+reorder+widget", http.StatusFound)
			return
		}

		slog.Info("widget moved up", "screen_id", screenID, "page_id", pageID, "widget_id", widgetID, "moved_by", user.Email)
		http.Redirect(w, r, pageEditURL+"?msg=widget_moved", http.StatusFound)
	}
}

func handleWidgetMoveDown(svc *screens.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		if user == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		screenID := r.PathValue("id")
		pageID := r.PathValue("pageID")
		widgetID := r.PathValue("widgetID")
		if screenID == "" || pageID == "" || widgetID == "" {
			http.Redirect(w, r, "/admin/screens?error=Missing+widget+ID", http.StatusFound)
			return
		}

		pageEditURL := "/admin/screens/" + screenID + "/pages/" + pageID + "/edit"
		if err := svc.MoveWidgetDown(ctx, screenID, pageID, widgetID); err != nil {
			if errors.Is(err, screens.ErrWidgetNotFound) {
				http.Redirect(w, r, pageEditURL+"?error=Widget+not+found", http.StatusFound)
				return
			}
			if errors.Is(err, screens.ErrPageNotFound) {
				http.Redirect(w, r, "/admin/screens/"+screenID+"/edit?error=Page+not+found", http.StatusFound)
				return
			}
			slog.Error("move widget down", "err", err, "screen_id", screenID, "page_id", pageID, "widget_id", widgetID)
			http.Redirect(w, r, pageEditURL+"?error=Could+not+reorder+widget", http.StatusFound)
			return
		}

		slog.Info("widget moved down", "screen_id", screenID, "page_id", pageID, "widget_id", widgetID, "moved_by", user.Email)
		http.Redirect(w, r, pageEditURL+"?msg=widget_moved", http.StatusFound)
	}
}
