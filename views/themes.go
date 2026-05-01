package views

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/jasoncorbett/screens/internal/auth"
	"github.com/jasoncorbett/screens/internal/themes"
)

// themeMsgText maps a flash code to a user-visible status message. Returns an
// empty string for unknown codes; the templ skips the status card when the
// text is empty.
func themeMsgText(code string) string {
	switch code {
	case "created":
		return "Theme created."
	case "updated":
		return "Theme updated."
	case "deleted":
		return "Theme deleted."
	case "set_default":
		return "Default theme changed."
	default:
		return ""
	}
}

// themeInputFromForm extracts a themes.Input from form values. The
// FontFamilyMono field is intentionally not surfaced in the v1 admin form.
func themeInputFromForm(r *http.Request) themes.Input {
	return themes.Input{
		Name:           r.FormValue("name"),
		ColorBg:        r.FormValue("color_bg"),
		ColorSurface:   r.FormValue("color_surface"),
		ColorBorder:    r.FormValue("color_border"),
		ColorText:      r.FormValue("color_text"),
		ColorTextMuted: r.FormValue("color_text_muted"),
		ColorAccent:    r.FormValue("color_accent"),
		FontFamily:     r.FormValue("font_family"),
		Radius:         r.FormValue("radius"),
	}
}

// themeInputFromTheme builds an Input from an existing Theme so the edit
// form pre-populates the user's current values.
func themeInputFromTheme(t themes.Theme) themes.Input {
	return themes.Input{
		Name:           t.Name,
		ColorBg:        t.ColorBg,
		ColorSurface:   t.ColorSurface,
		ColorBorder:    t.ColorBorder,
		ColorText:      t.ColorText,
		ColorTextMuted: t.ColorTextMuted,
		ColorAccent:    t.ColorAccent,
		FontFamily:     t.FontFamily,
		Radius:         t.Radius,
	}
}

func handleThemeList(themesSvc *themes.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		session := auth.SessionFromContext(ctx)

		if user == nil || session == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		list, err := themesSvc.List(ctx)
		if err != nil {
			slog.Error("list themes", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		msgCode := r.URL.Query().Get("msg")
		errMsg := r.URL.Query().Get("error")

		themesPage(list, user, session.CSRFToken, themeMsgText(msgCode), errMsg, themes.Input{}, nil).Render(ctx, w)
	}
}

func handleThemeCreate(themesSvc *themes.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		session := auth.SessionFromContext(ctx)

		if user == nil || session == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		in := themeInputFromForm(r)
		t, err := themesSvc.Create(ctx, in)
		if err == nil {
			slog.Info("theme created", "theme_id", t.ID, "name", t.Name, "created_by", user.Email)
			http.Redirect(w, r, "/admin/themes?msg=created", http.StatusFound)
			return
		}

		// Validation error: re-render the form inline with rejected values.
		var ve *themes.ValidationError
		if errors.As(err, &ve) {
			list, listErr := themesSvc.List(ctx)
			if listErr != nil {
				slog.Error("list themes after validation error", "err", listErr)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			themesPage(list, user, session.CSRFToken, "", "", in, ve.Fields).Render(ctx, w)
			return
		}

		// Duplicate name: surface as a per-field error on `name`.
		if errors.Is(err, themes.ErrDuplicateName) {
			list, listErr := themesSvc.List(ctx)
			if listErr != nil {
				slog.Error("list themes after duplicate name", "err", listErr)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			fields := map[string]string{"name": "a theme with this name already exists"}
			themesPage(list, user, session.CSRFToken, "", "", in, fields).Render(ctx, w)
			return
		}

		slog.Error("create theme", "err", err)
		http.Redirect(w, r, "/admin/themes?error=Could+not+create+theme", http.StatusFound)
	}
}

func handleThemeEditForm(themesSvc *themes.Service) http.HandlerFunc {
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
			http.Redirect(w, r, "/admin/themes?error=Missing+theme+ID", http.StatusFound)
			return
		}

		t, err := themesSvc.GetByID(ctx, id)
		if err != nil {
			if errors.Is(err, themes.ErrThemeNotFound) {
				http.Redirect(w, r, "/admin/themes?error=Theme+not+found", http.StatusFound)
				return
			}
			slog.Error("get theme for edit", "err", err, "theme_id", id)
			http.Redirect(w, r, "/admin/themes?error=Could+not+load+theme", http.StatusFound)
			return
		}

		themeEditPage(t, user, session.CSRFToken, themeInputFromTheme(t), nil).Render(ctx, w)
	}
}

func handleThemeUpdate(themesSvc *themes.Service) http.HandlerFunc {
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
			http.Redirect(w, r, "/admin/themes?error=Missing+theme+ID", http.StatusFound)
			return
		}

		in := themeInputFromForm(r)
		_, err := themesSvc.Update(ctx, id, in)
		if err == nil {
			slog.Info("theme updated", "theme_id", id, "updated_by", user.Email)
			http.Redirect(w, r, "/admin/themes?msg=updated", http.StatusFound)
			return
		}

		if errors.Is(err, themes.ErrThemeNotFound) {
			http.Redirect(w, r, "/admin/themes?error=Theme+not+found", http.StatusFound)
			return
		}

		// Validation / duplicate: re-render the edit form. Need to reload the
		// existing theme for the page header so the user knows which theme
		// they are editing.
		var ve *themes.ValidationError
		if errors.As(err, &ve) {
			existing, getErr := themesSvc.GetByID(ctx, id)
			if getErr != nil {
				if errors.Is(getErr, themes.ErrThemeNotFound) {
					http.Redirect(w, r, "/admin/themes?error=Theme+not+found", http.StatusFound)
					return
				}
				slog.Error("reload theme after validation error", "err", getErr, "theme_id", id)
				http.Redirect(w, r, "/admin/themes?error=Could+not+update+theme", http.StatusFound)
				return
			}
			themeEditPage(existing, user, session.CSRFToken, in, ve.Fields).Render(ctx, w)
			return
		}

		if errors.Is(err, themes.ErrDuplicateName) {
			existing, getErr := themesSvc.GetByID(ctx, id)
			if getErr != nil {
				if errors.Is(getErr, themes.ErrThemeNotFound) {
					http.Redirect(w, r, "/admin/themes?error=Theme+not+found", http.StatusFound)
					return
				}
				slog.Error("reload theme after duplicate name", "err", getErr, "theme_id", id)
				http.Redirect(w, r, "/admin/themes?error=Could+not+update+theme", http.StatusFound)
				return
			}
			fields := map[string]string{"name": "a theme with this name already exists"}
			themeEditPage(existing, user, session.CSRFToken, in, fields).Render(ctx, w)
			return
		}

		slog.Error("update theme", "err", err, "theme_id", id)
		http.Redirect(w, r, "/admin/themes?error=Could+not+update+theme", http.StatusFound)
	}
}

func handleThemeDelete(themesSvc *themes.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		if user == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		id := r.PathValue("id")
		if id == "" {
			http.Redirect(w, r, "/admin/themes?error=Missing+theme+ID", http.StatusFound)
			return
		}

		if err := themesSvc.Delete(ctx, id); err != nil {
			if errors.Is(err, themes.ErrThemeNotFound) {
				http.Redirect(w, r, "/admin/themes?error=Theme+not+found", http.StatusFound)
				return
			}
			if errors.Is(err, themes.ErrCannotDeleteDefault) {
				http.Redirect(w, r, "/admin/themes?error=Cannot+delete+the+default+theme", http.StatusFound)
				return
			}
			slog.Error("delete theme", "err", err, "theme_id", id)
			http.Redirect(w, r, "/admin/themes?error=Could+not+delete+theme", http.StatusFound)
			return
		}

		slog.Info("theme deleted", "theme_id", id, "deleted_by", user.Email)
		http.Redirect(w, r, "/admin/themes?msg=deleted", http.StatusFound)
	}
}

func handleThemeSetDefault(themesSvc *themes.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		if user == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		id := r.PathValue("id")
		if id == "" {
			http.Redirect(w, r, "/admin/themes?error=Missing+theme+ID", http.StatusFound)
			return
		}

		if err := themesSvc.SetDefault(ctx, id); err != nil {
			if errors.Is(err, themes.ErrThemeNotFound) {
				http.Redirect(w, r, "/admin/themes?error=Theme+not+found", http.StatusFound)
				return
			}
			slog.Error("set default theme", "err", err, "theme_id", id)
			http.Redirect(w, r, "/admin/themes?error=Could+not+set+default+theme", http.StatusFound)
			return
		}

		slog.Info("theme default changed", "theme_id", id, "changed_by", user.Email)
		http.Redirect(w, r, "/admin/themes?msg=set_default", http.StatusFound)
	}
}
